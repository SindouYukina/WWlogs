package instance

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// objectPrefix names the kernel objects of the lock. Both build profiles, every version, location and file name of the
// executable use it, so only the Windows user decides which lock applies.
const objectPrefix = "WWlogs.SingleInstance"

// identity names the lock and the pipe of one Windows user.
type identity struct {
	user  *windows.SID
	mutex string
	pipe  string
}

func identityFor(prefix string, user *windows.SID) identity {
	sid := user.String()
	return identity{user: user, mutex: `Global\` + prefix + "." + sid, pipe: `\\.\pipe\` + prefix + "." + sid}
}

// currentUser is the user of this process's token, which an elevated and a normal process of the user share.
func currentUser() (*windows.SID, error) {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read the Windows user of this process: %w", err)
	}
	return tokenUser.User.Sid.Copy()
}

// securityDescriptor makes the user the owner and grants the object to the user and SYSTEM only. The medium mandatory
// label with no-write-up gives elevated and normal processes of the user the same access and keeps low-integrity
// processes out.
func securityDescriptor(user *windows.SID) string {
	sid := user.String()
	return "O:" + sid + "D:P(A;;GA;;;" + sid + ")(A;;GA;;;SY)S:(ML;;NW;;;ME)"
}

func securityAttributes(user *windows.SID) (*windows.SecurityAttributes, error) {
	sd, err := windows.SecurityDescriptorFromString(securityDescriptor(user))
	if err != nil {
		return nil, fmt.Errorf("build the single-instance security descriptor: %w", err)
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

// ownedBy checks that the user owns an object. Another account cannot make the user the owner of an object it creates,
// so an object the user does not own did not come from this user's WWlogs.
func ownedBy(handle windows.Handle, user *windows.SID) error {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("could not be checked: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("could not be checked: %w", err)
	}
	if owner == nil || !windows.EqualSid(owner, user) {
		return errors.New("belongs to another account")
	}
	return nil
}

type lockState int

const (
	// lockHeld: another process owns the lock. It is sent once, before the final event.
	lockHeld lockState = iota
	// lockAcquired: this process owns the lock (final).
	lockAcquired
	// lockFailed: the lock could not be created, verified or waited on (final).
	lockFailed
)

type lockEvent struct {
	state lockState
	err   error
}

// holder is the thread that owns, or waits for, the lock. It sends at most two events: lockHeld, then a final one.
type holder struct {
	events chan lockEvent
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func startHolder(id identity, sa *windows.SecurityAttributes, slice time.Duration) *holder {
	h := &holder{events: make(chan lockEvent, 2), stop: make(chan struct{}), done: make(chan struct{})}
	go h.run(id, sa, max(slice, time.Millisecond))
	return h
}

// release gives up waiting for the lock, or releases the lock this process owns, and waits until the thread has closed
// the mutex.
func (h *holder) release() {
	h.once.Do(func() { close(h.stop) })
	<-h.done
}

// run creates the mutex and owns it on its own thread. Ownership of a mutex belongs to the thread that waited for it, so
// the goroutine keeps its thread locked and lets the thread end with it (it never calls UnlockOSThread): no other
// goroutine ever runs on a thread that owns the lock, and the lock is released only by ReleaseMutex here or by the end of
// the process. While another process owns it, the thread waits in slices so that stop is seen.
func (h *holder) run(id identity, sa *windows.SecurityAttributes, slice time.Duration) {
	runtime.LockOSThread()
	defer close(h.done)
	mutex, err := openLock(id, sa)
	if err != nil {
		h.events <- lockEvent{state: lockFailed, err: err}
		return
	}
	defer windows.CloseHandle(mutex)
	reported := false
	for {
		event, err := windows.WaitForSingleObject(mutex, uint32(slice/time.Millisecond))
		switch event {
		case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
			// WAIT_ABANDONED: the previous owner ended without releasing the lock. The ownership passes on all the same.
			h.events <- lockEvent{state: lockAcquired}
			<-h.stop
			windows.ReleaseMutex(mutex)
			return
		case uint32(windows.WAIT_TIMEOUT):
			if !reported {
				reported = true
				h.events <- lockEvent{state: lockHeld}
			}
			select {
			case <-h.stop:
				return
			default:
			}
		default:
			h.events <- lockEvent{state: lockFailed, err: fmt.Errorf("wait for the single-instance lock: %w", err)}
			return
		}
	}
}

// openLock creates the mutex, or opens it when it already exists and belongs to this user. Every other state is a
// failure of its own: an object of the name that denies this user, an object of another type, or a system error.
func openLock(id identity, sa *windows.SecurityAttributes) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(id.mutex)
	if err != nil {
		return 0, err
	}
	mutex, err := windows.CreateMutex(sa, false, name)
	switch {
	case err == nil:
		return mutex, nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		if err := ownedBy(mutex, id.user); err != nil {
			windows.CloseHandle(mutex)
			return 0, fmt.Errorf("the single-instance lock %w", err)
		}
		return mutex, nil
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return 0, errors.New("an object with the single-instance lock's name denies this user access")
	case errors.Is(err, windows.ERROR_INVALID_HANDLE):
		return 0, errors.New("an object of another type has the single-instance lock's name")
	default:
		return 0, fmt.Errorf("create the single-instance lock: %w", err)
	}
}
