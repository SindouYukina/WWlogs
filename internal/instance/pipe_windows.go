package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procAllowSetForegroundWindow    = user32.NewProc("AllowSetForegroundWindow")
	procGetProcessWindowStation     = user32.NewProc("GetProcessWindowStation")
	procGetThreadDesktop            = user32.NewProc("GetThreadDesktop")
	procGetUserObjectInformation    = user32.NewProc("GetUserObjectInformationW")
	procGetNamedPipeClientSessionId = kernel32.NewProc("GetNamedPipeClientSessionId")
	procWaitNamedPipe               = kernel32.NewProc("WaitNamedPipeW")
)

const (
	// ioLimit bounds one read or write of a request or an answer, which are a few hundred bytes at most.
	ioLimit = 2 * time.Second
	// pipeCreateLimit bounds retrying the pipe: an instance that has just ended may still hold it for a moment after
	// its lock came free, when Windows closes the handles of a killed process in another order.
	pipeCreateLimit = 2 * time.Second
	// failurePause keeps a pipe that keeps failing from spinning the server.
	failurePause = 100 * time.Millisecond
)

var (
	errStopped = errors.New("the activation channel was closed")
	errTimeout = errors.New("timed out")
)

// server answers later launches on the pipe. It has one pipe instance and serves one launch at a time; the others wait
// in WaitNamedPipe. It waits without polling: a blocked wait for a connection, and nothing at all between launches.
type server struct {
	pipe    windows.Handle
	stop    windows.Handle
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	handler Handler
	// session and desktop are where this instance runs; a launch from anywhere else is answered Elsewhere.
	session uint32
	desktop string
}

func startServer(id identity, sa *windows.SecurityAttributes, handler Handler) (*server, error) {
	if handler == nil {
		return nil, errors.New("no handler for later launches")
	}
	pipe, err := createPipe(id.pipe, sa)
	if err != nil {
		return nil, err
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(pipe)
		return nil, fmt.Errorf("create the activation channel's stop event: %w", err)
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		windows.CloseHandle(pipe)
		windows.CloseHandle(stop)
		return nil, fmt.Errorf("read the Windows session of this process: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &server{pipe: pipe, stop: stop, ctx: ctx, cancel: cancel, done: make(chan struct{}), handler: handler, session: session, desktop: currentDesktop()}
	go s.run()
	return s, nil
}

// createPipe creates the one pipe instance. FILE_FLAG_FIRST_PIPE_INSTANCE refuses a name another process already
// serves, so nothing else can answer in this instance's place unnoticed, and remote clients are rejected.
func createPipe(name string, sa *windows.SecurityAttributes) (windows.Handle, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	deadline := time.Now().Add(pipeCreateLimit)
	for {
		pipe, err := windows.CreateNamedPipe(path,
			windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
			1, 64, 1024, 0, sa)
		if err == nil {
			return pipe, nil
		}
		retry := errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY)
		if !retry || !time.Now().Before(deadline) {
			return 0, fmt.Errorf("create the activation channel: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *server) run() {
	defer close(s.done)
	io, err := newOverlapped(s.pipe, s.stop)
	if err != nil {
		return
	}
	defer io.close()
	for {
		err := io.connect()
		if errors.Is(err, errStopped) {
			return
		}
		if err == nil {
			s.answer(io)
		} else if event, _ := windows.WaitForSingleObject(s.stop, uint32(failurePause/time.Millisecond)); event == windows.WAIT_OBJECT_0 {
			return
		}
		windows.DisconnectNamedPipe(s.pipe)
	}
}

// answer reads one request and answers it. A request that is not a WWlogs request, or comes too slowly, gets no answer.
func (s *server) answer(io *overlapped) {
	header := make([]byte, requestHeaderSize)
	if io.readFull(header, ioLimit) != nil {
		return
	}
	length, err := decodeRequestHeader(header)
	if err != nil {
		return
	}
	desktop := make([]byte, length)
	if io.readFull(desktop, ioLimit) != nil {
		return
	}
	reply := Elsewhere
	if s.fromHere(string(desktop)) {
		ctx, cancel := context.WithTimeout(s.ctx, answerLimit)
		reply = s.handler(ctx)
		cancel()
	}
	if reply == NoReply || io.write([]byte{byte(reply)}, ioLimit) != nil {
		return
	}
	// Disconnecting discards what the launch has not read yet, so wait until it closes its end.
	io.readFull(make([]byte, 1), ioLimit)
}

// fromHere reports whether the connected launch runs in this instance's Windows session and on its desktop. Only then may
// the window be brought forward. A session that cannot be read counts as another one.
func (s *server) fromHere(desktop string) bool {
	var session uint32
	ok, _, _ := procGetNamedPipeClientSessionId.Call(uintptr(s.pipe), uintptr(unsafe.Pointer(&session)))
	return ok != 0 && samePlace(session, desktop, s.session, s.desktop)
}

// samePlace compares where a launch runs with where this instance runs. Window station and desktop names are not case
// sensitive.
func samePlace(session uint32, desktop string, ownSession uint32, ownDesktop string) bool {
	return session == ownSession && strings.EqualFold(desktop, ownDesktop)
}

// close ends a running answer, stops the server and closes the pipe.
func (s *server) close() {
	s.cancel()
	windows.SetEvent(s.stop)
	<-s.done
	windows.CloseHandle(s.pipe)
	windows.CloseHandle(s.stop)
}

// ask sends one request to the running instance and returns its answer.
func ask(id identity, desktop string, deadline time.Time, replyLimit time.Duration) (Reply, error) {
	pipe, err := dial(id.pipe, deadline)
	if err != nil {
		return NoReply, fmt.Errorf("reach the running instance: %w", err)
	}
	defer windows.CloseHandle(pipe)
	if err := ownedBy(pipe, id.user); err != nil {
		return NoReply, fmt.Errorf("the activation channel %w", err)
	}
	// Windows lets this launch take the foreground, because the user started it; pass that on to the running instance.
	// Without it, Windows may keep the foreground elsewhere, and the running instance flashes its taskbar button.
	var server uint32
	if windows.GetNamedPipeServerProcessId(pipe, &server) == nil {
		procAllowSetForegroundWindow.Call(uintptr(server))
	}
	io, err := newOverlapped(pipe, 0)
	if err != nil {
		return NoReply, err
	}
	defer io.close()
	if err := io.write(encodeRequest(desktop), ioLimit); err != nil {
		return NoReply, fmt.Errorf("send the request: %w", err)
	}
	answer := make([]byte, 1)
	if err := io.readFull(answer, replyLimit); err != nil {
		return NoReply, fmt.Errorf("read the answer: %w", err)
	}
	return Reply(answer[0]), nil
}

// dial connects to the pipe, waiting while another launch is connected to it. The connection allows the running
// instance to identify this launch but not to impersonate it.
func dial(name string, deadline time.Time) (windows.Handle, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	for {
		pipe, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			return pipe, nil
		}
		wait := time.Until(deadline)
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) || wait <= 0 {
			return 0, err
		}
		// WaitNamedPipe returns as soon as the pipe is free, or fails at its limit; either way, try again.
		procWaitNamedPipe.Call(uintptr(unsafe.Pointer(path)), uintptr(min(wait, time.Second)/time.Millisecond))
	}
}

// overlapped runs one I/O at a time on a handle opened for overlapped I/O, each with a time limit and, on the server, the
// stop event. The kernel writes to the OVERLAPPED structure and the buffer until the operation ends, so every operation
// is completed, or cancelled and waited for, before it returns; both live on the heap.
type overlapped struct {
	handle windows.Handle
	stop   windows.Handle
	ov     *windows.Overlapped
}

func newOverlapped(handle, stop windows.Handle) (*overlapped, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("create an I/O event: %w", err)
	}
	return &overlapped{handle: handle, stop: stop, ov: &windows.Overlapped{HEvent: event}}, nil
}

func (o *overlapped) close() { windows.CloseHandle(o.ov.HEvent) }

func (o *overlapped) reset() {
	event := o.ov.HEvent
	*o.ov = windows.Overlapped{HEvent: event}
	windows.ResetEvent(event)
}

// connect waits until a launch connects or the server stops.
func (o *overlapped) connect() error {
	o.reset()
	err := windows.ConnectNamedPipe(o.handle, o.ov)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		// ERROR_PIPE_CONNECTED: the launch connected before the call; nothing is pending.
		return nil
	case errors.Is(err, windows.ERROR_IO_PENDING):
		_, err := o.wait(0)
		return err
	default:
		return err
	}
}

func (o *overlapped) readFull(buffer []byte, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for len(buffer) > 0 {
		left := time.Until(deadline)
		if left <= 0 {
			return errTimeout
		}
		o.reset()
		var done uint32
		err := windows.ReadFile(o.handle, buffer, &done, o.ov)
		if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
			return err
		}
		n, err := o.wait(left)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("the other side closed the channel")
		}
		buffer = buffer[n:]
	}
	return nil
}

func (o *overlapped) write(data []byte, limit time.Duration) error {
	o.reset()
	var done uint32
	err := windows.WriteFile(o.handle, data, &done, o.ov)
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return err
	}
	n, err := o.wait(limit)
	if err == nil && int(n) != len(data) {
		err = errors.New("short write")
	}
	return err
}

// wait waits for the started operation: at most limit (0: no limit), and on the server until stop is set. An operation
// that does not end in time is cancelled, and its cancellation waited for.
func (o *overlapped) wait(limit time.Duration) (uint32, error) {
	handles := []windows.Handle{o.ov.HEvent}
	if o.stop != 0 {
		handles = append(handles, o.stop)
	}
	timeout := uint32(windows.INFINITE)
	if limit > 0 {
		timeout = uint32(max(limit/time.Millisecond, 1))
	}
	event, waitErr := windows.WaitForMultipleObjects(handles, false, timeout)
	if event != windows.WAIT_OBJECT_0 {
		windows.CancelIoEx(o.handle, o.ov)
	}
	var done uint32
	err := windows.GetOverlappedResult(o.handle, o.ov, &done, true)
	switch {
	case event == windows.WAIT_OBJECT_0 || err == nil:
		// A cancelled operation may still have completed just before the cancellation.
		return done, err
	case event == uint32(windows.WAIT_TIMEOUT):
		return done, errTimeout
	case event == windows.WAIT_OBJECT_0+1:
		return done, errStopped
	default:
		return done, fmt.Errorf("wait for the activation channel: %w", waitErr)
	}
}

// currentDesktop names the window station and desktop this process runs on, such as `WinSta0\Default`.
func currentDesktop() string {
	station, _, _ := procGetProcessWindowStation.Call()
	desktop, _, _ := procGetThreadDesktop.Call(uintptr(windows.GetCurrentThreadId()))
	return objectName(station) + `\` + objectName(desktop)
}

// objectName reads the name of a window station or desktop. The handles come from Get* functions and are not closed.
func objectName(handle uintptr) string {
	const uoiName = 2
	if handle == 0 {
		return ""
	}
	var name [256]uint16
	var needed uint32
	ok, _, _ := procGetUserObjectInformation.Call(handle, uoiName, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)*2), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return ""
	}
	return windows.UTF16ToString(name[:])
}

// Interactive reports whether this process runs on a window station the user can see, where a message box can be
// answered. A process that a service or a background task started cannot show one.
func Interactive() bool {
	const uoiFlags, wsfVisible = 1, 1
	station, _, _ := procGetProcessWindowStation.Call()
	if station == 0 {
		return false
	}
	var flags struct {
		inherit, reserved int32
		flags             uint32
	}
	var needed uint32
	ok, _, _ := procGetUserObjectInformation.Call(station, uoiFlags, uintptr(unsafe.Pointer(&flags)), unsafe.Sizeof(flags), uintptr(unsafe.Pointer(&needed)))
	return ok != 0 && flags.flags&wsfVisible != 0
}
