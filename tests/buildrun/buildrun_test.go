package buildrun_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"wwlogs/scripts/buildrun"
)

// Every test uses its own temporary counter directory; the owner's .local counter is never touched.

func readState(t *testing.T, dir string) buildrun.State {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "build-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state buildrun.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

// completeRun runs one successful build run against the counter and returns its number.
func completeRun(t *testing.T, store *buildrun.Store, initialize bool) uint64 {
	t.Helper()
	allocation, err := store.Begin(initialize)
	if err != nil {
		t.Fatal(err)
	}
	if err := allocation.MarkIssued(); err != nil {
		t.Fatal(err)
	}
	if err := allocation.Commit(buildrun.SuccessRecord{Commit: "c", Dirty: "false", Profiles: []string{"development", "public"}}); err != nil {
		t.Fatal(err)
	}
	return allocation.Number
}

func TestMissingCounterIsUnofficialAndCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".local")
	_, err := (&buildrun.Store{Dir: dir}).Begin(false)
	if !errors.Is(err, buildrun.ErrNotInitialized) {
		t.Fatalf("Begin without a counter = %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("an unofficial build created the counter directory: %v", statErr)
	}
}

func TestFirstInitializationStartsAtOne(t *testing.T) {
	dir := t.TempDir()
	store := &buildrun.Store{Dir: dir}
	allocation, err := store.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	if allocation.Number != 1 || !allocation.Initialized {
		t.Fatalf("first allocation = %d initialized=%t", allocation.Number, allocation.Initialized)
	}
	// Nothing is written before the run reaches installation.
	if _, err := os.Stat(filepath.Join(dir, "build-state.json")); !os.IsNotExist(err) {
		t.Fatalf("state written before installation: %v", err)
	}
	allocation.MarkIssued()
	if err := allocation.Commit(buildrun.SuccessRecord{Commit: "c", Dirty: "false"}); err != nil {
		t.Fatal(err)
	}
	state := readState(t, dir)
	if state.LastSuccessfulBuildNumber != 1 || state.HighestIssuedBuildNumber != 1 || state.LastSuccessful.BuildNumber != 1 {
		t.Fatalf("state after first run: %+v", state)
	}
	if _, err := os.Stat(filepath.Join(dir, "build-state.backup.json")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestInitializationIsRefusedWhenACounterExistsOrExisted(t *testing.T) {
	dir := t.TempDir()
	store := &buildrun.Store{Dir: dir}
	completeRun(t, store, true)
	if _, err := store.Begin(true); !errors.Is(err, buildrun.ErrBlocked) {
		t.Fatalf("re-initialization over an existing counter = %v", err)
	}
	// A lost state file next to its backup must not be re-initialized to 1.
	os.Remove(filepath.Join(dir, "build-state.json"))
	if _, err := store.Begin(true); !errors.Is(err, buildrun.ErrBlocked) {
		t.Fatalf("initialization with only a backup = %v", err)
	}
	if _, err := store.Begin(false); !errors.Is(err, buildrun.ErrNotInitialized) {
		t.Fatalf("official run with a lost state file = %v", err)
	}
}

func TestCorruptCounterIsBlockedAndKept(t *testing.T) {
	for name, content := range map[string]string{
		"invalid JSON":   "{not json",
		"unknown schema": `{"schemaVersion":9,"lastSuccessfulBuildNumber":3,"highestIssuedBuildNumber":3}`,
		"issued below":   `{"schemaVersion":1,"lastSuccessfulBuildNumber":5,"highestIssuedBuildNumber":4,"lastSuccessful":{"buildNumber":5}}`,
		"record missing": `{"schemaVersion":1,"lastSuccessfulBuildNumber":5,"highestIssuedBuildNumber":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "build-state.json")
			os.WriteFile(path, []byte(content), 0o600)
			if _, err := (&buildrun.Store{Dir: dir}).Begin(false); !errors.Is(err, buildrun.ErrBlocked) {
				t.Fatalf("Begin = %v", err)
			}
			if _, err := (&buildrun.Store{Dir: dir}).Begin(true); !errors.Is(err, buildrun.ErrBlocked) {
				t.Fatalf("Begin(initialize) = %v", err)
			}
			if data, _ := os.ReadFile(path); string(data) != content {
				t.Fatal("a corrupt counter was rewritten")
			}
		})
	}
}

func TestEverySuccessfulRunAdvancesEvenFromTheSameSource(t *testing.T) {
	store := &buildrun.Store{Dir: t.TempDir()}
	for want := uint64(1); want <= 3; want++ {
		if got := completeRun(t, store, want == 1); got != want {
			t.Fatalf("run %d got Build Number %d", want, got)
		}
	}
}

func TestFailedRunsDoNotAdvance(t *testing.T) {
	dir := t.TempDir()
	store := &buildrun.Store{Dir: dir}
	completeRun(t, store, true)

	// Failure while building: nothing was issued.
	allocation, _ := store.Begin(false)
	if allocation.Number != 2 {
		t.Fatalf("candidate = %d", allocation.Number)
	}
	allocation.Abort(true)
	// Failure while installing, fully rolled back: the number is free again.
	allocation, _ = store.Begin(false)
	allocation.MarkIssued()
	allocation.Abort(true)
	if state := readState(t, dir); state.LastSuccessfulBuildNumber != 1 || state.HighestIssuedBuildNumber != 1 {
		t.Fatalf("state after rolled-back failure: %+v", state)
	}
	if got := completeRun(t, store, false); got != 2 {
		t.Fatalf("next run after failures = %d, want 2", got)
	}

	// Failure while installing without a complete rollback: the number may exist, so it is skipped.
	allocation, _ = store.Begin(false)
	allocation.MarkIssued()
	allocation.Abort(false)
	if state := readState(t, dir); state.LastSuccessfulBuildNumber != 2 || state.HighestIssuedBuildNumber != 3 {
		t.Fatalf("state after partial install: %+v", state)
	}
	if got := completeRun(t, store, false); got != 4 {
		t.Fatalf("run after a partial install = %d, want 4", got)
	}
}

func TestConcurrentRunsGetDistinctNumbers(t *testing.T) {
	store := &buildrun.Store{Dir: t.TempDir()}
	completeRun(t, store, true)
	const runs = 8
	var mu sync.Mutex
	var numbers []int
	var wg sync.WaitGroup
	for range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allocation, err := (&buildrun.Store{Dir: store.Dir}).Begin(false)
			if err != nil {
				t.Error(err)
				return
			}
			time.Sleep(5 * time.Millisecond)
			if err := allocation.MarkIssued(); err != nil {
				t.Error(err)
			}
			if err := allocation.Commit(buildrun.SuccessRecord{}); err != nil {
				t.Error(err)
			}
			mu.Lock()
			numbers = append(numbers, int(allocation.Number))
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Ints(numbers)
	for index, number := range numbers {
		if number != index+2 {
			t.Fatalf("numbers = %v, want 2..%d without repeats", numbers, runs+1)
		}
	}
}

func TestLockTimeoutReportsTheOtherRun(t *testing.T) {
	dir := t.TempDir()
	store := &buildrun.Store{Dir: dir}
	completeRun(t, store, true)
	holder, err := store.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Abort(true)
	waited := false
	_, err = (&buildrun.Store{Dir: dir, LockTimeout: 300 * time.Millisecond, Waiting: func() { waited = true }}).Begin(false)
	if err == nil || !waited {
		t.Fatalf("second run while locked: err=%v waited=%t", err, waited)
	}
}

// TestKilledRunReleasesTheLockAndItsNumberIsNotReused kills a helper process that holds the lock after recording
// its number as issued, as an interruption during installation would.
func TestKilledRunReleasesTheLockAndItsNumberIsNotReused(t *testing.T) {
	dir := t.TempDir()
	store := &buildrun.Store{Dir: dir}
	completeRun(t, store, true)
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperHoldLock$")
	helper.Env = append(os.Environ(), "WWLOGS_BUILDRUN_HELPER_DIR="+dir)
	output, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	n, _ := output.Read(buffer)
	if string(buffer[:n]) != "issued 2\n" {
		helper.Process.Kill()
		t.Fatalf("helper said %q", buffer[:n])
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	helper.Wait()
	allocation, err := (&buildrun.Store{Dir: dir, LockTimeout: 5 * time.Second}).Begin(false)
	if err != nil {
		t.Fatalf("lock after the holder was killed: %v", err)
	}
	defer allocation.Abort(true)
	if allocation.Number != 3 {
		t.Fatalf("number after an interrupted install = %d, want 3", allocation.Number)
	}
}

func TestHelperHoldLock(t *testing.T) {
	dir := os.Getenv("WWLOGS_BUILDRUN_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process only")
	}
	allocation, err := (&buildrun.Store{Dir: dir}).Begin(false)
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	allocation.MarkIssued()
	fmt.Printf("issued %d\n", allocation.Number)
	time.Sleep(time.Minute)
}

// fakeBuilder writes the profile and Build Number into the staged file, or fails for one profile.
func fakeBuilder(number uint64, failProfile string) buildrun.Builder {
	return func(artifact buildrun.Artifact, staged string) error {
		if artifact.Profile == failProfile {
			return errors.New("injected build failure")
		}
		return os.WriteFile(staged, []byte(artifact.Profile+" build "+strconv.FormatUint(number, 10)), 0o600)
	}
}

func artifactsIn(root string) []buildrun.Artifact {
	return []buildrun.Artifact{
		{Profile: "development", Output: filepath.Join(root, "development", "WWlogs.exe")},
		{Profile: "public", Output: filepath.Join(root, "public", "WWlogs.exe")},
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestOneRunInstallsBothProfilesWithOneNumber(t *testing.T) {
	root := t.TempDir()
	store := &buildrun.Store{Dir: filepath.Join(root, ".local")}
	allocation, _ := store.Begin(true)
	staged, err := buildrun.Stage(artifactsIn(root), fakeBuilder(allocation.Number, ""))
	if err != nil {
		t.Fatal(err)
	}
	allocation.MarkIssued()
	installation, _, err := staged.Install()
	if err != nil {
		t.Fatal(err)
	}
	if err := allocation.Commit(buildrun.SuccessRecord{}); err != nil {
		t.Fatal(err)
	}
	installation.Finalize()
	for _, profile := range []string{"development", "public"} {
		data, _ := os.ReadFile(filepath.Join(root, profile, "WWlogs.exe"))
		if string(data) != profile+" build 1" {
			t.Fatalf("%s artifact = %q", profile, data)
		}
		if names := listDir(t, filepath.Join(root, profile)); len(names) != 1 {
			t.Fatalf("%s directory holds %v", profile, names)
		}
	}
}

func TestFailedProfileInstallsNothingAndKeepsPreviousArtifacts(t *testing.T) {
	root := t.TempDir()
	for _, artifact := range artifactsIn(root) {
		os.MkdirAll(filepath.Dir(artifact.Output), 0o755)
		os.WriteFile(artifact.Output, []byte("previous"), 0o600)
	}
	// Development builds, Public fails: the run fails as a whole.
	if _, err := buildrun.Stage(artifactsIn(root), fakeBuilder(7, "public")); err == nil {
		t.Fatal("a run with a failed profile succeeded")
	}
	for _, profile := range []string{"development", "public"} {
		data, _ := os.ReadFile(filepath.Join(root, profile, "WWlogs.exe"))
		if string(data) != "previous" {
			t.Fatalf("%s artifact replaced by a failed run: %q", profile, data)
		}
		if names := listDir(t, filepath.Join(root, profile)); len(names) != 1 {
			t.Fatalf("%s directory holds staged leftovers: %v", profile, names)
		}
	}
}

func TestRollbackRestoresPreviousArtifacts(t *testing.T) {
	root := t.TempDir()
	artifacts := artifactsIn(root)
	os.MkdirAll(filepath.Dir(artifacts[0].Output), 0o755)
	os.WriteFile(artifacts[0].Output, []byte("previous"), 0o600)
	staged, err := buildrun.Stage(artifacts, fakeBuilder(9, ""))
	if err != nil {
		t.Fatal(err)
	}
	installation, _, err := staged.Install()
	if err != nil {
		t.Fatal(err)
	}
	// The counter could not be recorded: the run is undone.
	if err := installation.Rollback(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(artifacts[0].Output); string(data) != "previous" {
		t.Fatalf("development after rollback = %q", data)
	}
	if _, err := os.Stat(artifacts[1].Output); !os.IsNotExist(err) {
		t.Fatalf("public after rollback: %v", err)
	}
	if names := listDir(t, filepath.Dir(artifacts[0].Output)); len(names) != 1 {
		t.Fatalf("leftovers after rollback: %v", names)
	}
}

func TestInstallFailureRestoresEarlierProfiles(t *testing.T) {
	root := t.TempDir()
	artifacts := artifactsIn(root)
	os.MkdirAll(filepath.Dir(artifacts[0].Output), 0o755)
	os.WriteFile(artifacts[0].Output, []byte("previous"), 0o600)
	staged, err := buildrun.Stage(artifacts, fakeBuilder(4, ""))
	if err != nil {
		t.Fatal(err)
	}
	// An open handle on the previous Public executable (Go opens files without FILE_SHARE_DELETE) stops it from being
	// moved aside, so its installation fails after Development was installed.
	os.WriteFile(artifacts[1].Output, []byte("previous public"), 0o600)
	busy, err := os.Open(artifacts[1].Output)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if _, rolledBack, err := staged.Install(); err == nil || !rolledBack {
		t.Fatalf("Install = rolledBack %t, %v", rolledBack, err)
	}
	if data, _ := os.ReadFile(artifacts[0].Output); string(data) != "previous" {
		t.Fatalf("development after a failed install = %q", data)
	}
	if data, _ := os.ReadFile(artifacts[1].Output); string(data) != "previous public" {
		t.Fatalf("public after a failed install = %q", data)
	}
	for _, artifact := range artifacts {
		if names := listDir(t, filepath.Dir(artifact.Output)); len(names) != 1 {
			t.Fatalf("leftovers after a failed install: %v", names)
		}
	}
}

func TestStageRejectsSharedOutputs(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "WWlogs.exe")
	_, err := buildrun.Stage([]buildrun.Artifact{{Profile: "development", Output: output}, {Profile: "public", Output: output}}, fakeBuilder(1, ""))
	if err == nil {
		t.Fatal("two profiles were allowed to overwrite one output")
	}
}
