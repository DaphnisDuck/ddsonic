package ytdlp_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlp"
	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

// What the fix is for, and that the fake shows it: a yt-dlp killed or
// terminated while it holds its copy of the cookie database leaves the copy
// behind, and one that is interrupted removes it.
func TestFakeLeavesItsCopyWhenKilledAndRemovesItWhenInterrupted(t *testing.T) {
	tests := []struct {
		name     string
		stop     func(*exec.Cmd) error
		wantLeft bool
	}{
		{"killed", func(c *exec.Cmd) error { return c.Process.Kill() }, true},
		{"terminated", func(c *exec.Cmd) error { return c.Process.Signal(syscall.SIGTERM) }, true},
		{"interrupted", func(c *exec.Cmd) error { return c.Process.Signal(os.Interrupt) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
			cmd := exec.Command("yt-dlp", "--", "https://example.invalid/v")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			fake.WaitCopying(t)
			if err := tt.stop(cmd); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			if left := fake.CopiesLeft(t); (len(left) > 0) != tt.wantLeft {
				t.Errorf("copies left behind: %q, want some = %v", left, tt.wantLeft)
			}
		})
	}
}

func TestCommandRunsYTDLPWithArgs(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, "out\n")
	cmd, release := command(t, context.Background(), "-j", "--cookies-from-browser", "firefox", "--", "https://example.invalid/v")
	out, err := cmd.Output()
	if rerr := release(); rerr != nil {
		t.Errorf("release: %v", rerr)
	}
	if err != nil || string(out) != "out\n" {
		t.Fatalf("Output = %q, %v; want the run's output", out, err)
	}
	want := "--ignore-config --no-mark-watched -j --cookies-from-browser firefox -- https://example.invalid/v"
	if runs := fake.Runs(t); !slices.Equal(runs, []string{want}) {
		t.Errorf("yt-dlp ran with %q, want %q", runs, want)
	}
	fake.RequireNoCopy(t)
	fake.RequireOwnTemp(t)
}

// However a run ends, nothing of it stays in temporary storage, and nothing
// that was there before goes.
func TestCommandLeavesNoCopy(t *testing.T) {
	tests := []struct {
		name string
		mode string
		// ctx returns the run's context and what ends it early, if anything.
		ctx func() (context.Context, context.CancelFunc, bool)
		// The run must be over no sooner and no later than this after it was
		// told to stop.
		atLeast, atMost time.Duration
		// Whether yt-dlp was interrupted, and so cleaned up itself.
		wantInterrupted bool
	}{
		{"finishes", ytdlptest.Finish, background, 0, 2 * time.Second, false},
		{"cancelled while copying", ytdlptest.Hang, cancellable, 0, 2 * time.Second, true},
		{"times out while copying", ytdlptest.Hang, timingOut, 0, 2 * time.Second, true},
		// One that will not stop is killed after Grace, and its directory
		// is removed with the copy in it.
		{"ignores the request to stop", ytdlptest.Stubborn, cancellable, ytdlp.Grace, ytdlp.Grace + 2*time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, tt.mode, "")
			ctx, cancel, byHand := tt.ctx()
			defer cancel()
			cmd, release := command(t, ctx, "--", "https://example.invalid/v")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			fake.WaitCopying(t)
			if byHand {
				cancel()
			}
			start := time.Now()
			err := cmd.Wait()
			took := time.Since(start)
			if rerr := release(); rerr != nil {
				t.Errorf("release: %v", rerr)
			}

			if tt.mode != ytdlptest.Finish && err == nil {
				t.Error("a run that was stopped reported no error")
			}
			if took < tt.atLeast || took > tt.atMost {
				t.Errorf("the run was over %v after it was told to stop, want between %v and %v", took, tt.atLeast, tt.atMost)
			}
			if got := fake.Interrupted(t); got != tt.wantInterrupted {
				t.Errorf("yt-dlp was interrupted = %v, want %v", got, tt.wantInterrupted)
			}
			fake.RequireNoCopy(t)
			fake.RequireOwnTemp(t)
		})
	}
}

func background() (context.Context, context.CancelFunc, bool) {
	return context.Background(), func() {}, false
}

func cancellable() (context.Context, context.CancelFunc, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	return ctx, cancel, true
}

func timingOut() (context.Context, context.CancelFunc, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	return ctx, cancel, false
}

// release is the caller's to call whether or not the run started, and more
// than once does no harm.
func TestCommandReleaseWithoutARun(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, "")
	_, release := command(t, context.Background(), "--version")
	for range 2 {
		if err := release(); err != nil {
			t.Errorf("release: %v", err)
		}
	}
	if left := fake.CopiesLeft(t); len(left) != 0 {
		t.Errorf("left in temporary storage: %q", left)
	}
}

// A run that was never told to stop is not cut short by Grace.
func TestCommandDoesNotStopARunThatIsNotCancelled(t *testing.T) {
	ytdlptest.InstallCopier(t, ytdlptest.Finish, "done\n")
	cmd, release := command(t, context.Background(), "--", "https://example.invalid/v")
	defer release()
	if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != "done" {
		t.Errorf("Output = %q, %v; want done", out, err)
	}
}

// command is ytdlp.Command, which must succeed.
func command(t *testing.T, ctx context.Context, args ...string) (*exec.Cmd, func() error) {
	t.Helper()
	cmd, release, err := ytdlp.Command(ctx, args...)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	return cmd, release
}

// Without a directory of its own a run would copy the cookies wherever
// yt-dlp falls back to. There is no run.
func TestCommandRefusesToRunWithoutATemporaryDirectoryOfItsOwn(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, "")
	fake.DenyTemp(t)
	cmd, release, err := ytdlp.Command(context.Background(), "--", "https://example.invalid/v")
	if err == nil || cmd != nil || release != nil {
		t.Fatalf("Command = %v, release set = %v, %v; want only an error", cmd, release != nil, err)
	}
	if !strings.Contains(err.Error(), "not run") {
		t.Errorf("error = %q, want it to say yt-dlp is not run", err)
	}
}

// A directory that cannot be removed is reported each time, and removed once
// it can be. Nothing outside it is touched to get there.
func TestReleaseReportsADirectoryItCannotRemove(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Unremovable, "")
	cmd, release := command(t, context.Background(), "--", "https://example.invalid/v")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		err := release()
		if err == nil || !strings.Contains(err.Error(), "could not be removed") {
			t.Fatalf("release = %v, want it to name the directory that could not be removed", err)
		}
	}
	left := fake.CopiesLeft(t)
	if len(left) != 1 || !strings.Contains(release().Error(), left[0]) {
		t.Errorf("left in temporary storage: %q, want the one directory the error names", left)
	}
	fake.Unlock(t)
	if err := release(); err != nil {
		t.Errorf("release once the directory can be emptied: %v", err)
	}
	fake.RequireNoCopy(t)
}

func TestFinishAddsTheReleaseErrorToTheFunctionsOwn(t *testing.T) {
	own, left := errors.New("own"), errors.New("left")
	tests := []struct {
		name         string
		err, release error
		want         []error
	}{
		{"both fine", nil, nil, nil},
		{"only the function failed", own, nil, []error{own}},
		{"only the release failed", nil, left, []error{left}},
		{"both failed", own, left, []error{own, left}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.err
			ytdlp.Finish(func() error { return tt.release }, &err)
			if (err == nil) != (len(tt.want) == 0) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			for _, want := range tt.want {
				if !errors.Is(err, want) {
					t.Errorf("err = %v, want it to hold %v", err, want)
				}
			}
		})
	}
}

// Quitting stops the runs nobody else is waiting for: each is interrupted,
// reaped by its caller and released, and Shutdown returns when they are.
func TestShutdownStopsRunsAndWaitsForTheirRelease(t *testing.T) {
	tests := []struct {
		name            string
		mode            string
		atLeast, atMost time.Duration
		wantInterrupted bool
	}{
		{"a run that stops when asked", ytdlptest.Hang, 0, 2 * time.Second, true},
		{"a run that ignores the request", ytdlptest.Stubborn, ytdlp.Grace, ytdlp.Grace + 2*time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, tt.mode, "")
			t.Cleanup(ytdlp.Reopen)
			// Nothing ends this run but quitting: its context never does.
			cmd, release := command(t, context.Background(), "--", "https://example.invalid/v")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := make(chan error, 1)
			go func() {
				_ = cmd.Wait()
				waited <- release()
			}()
			fake.WaitCopying(t)

			start := time.Now()
			err := ytdlp.Shutdown()
			took := time.Since(start)
			if err != nil {
				t.Errorf("Shutdown: %v", err)
			}
			if took < tt.atLeast || took > tt.atMost {
				t.Errorf("Shutdown took %v, want between %v and %v", took, tt.atLeast, tt.atMost)
			}
			select {
			case rerr := <-waited:
				if rerr != nil {
					t.Errorf("release: %v", rerr)
				}
			default:
				t.Error("Shutdown returned before the run was reaped and released")
			}
			if got := fake.Interrupted(t); got != tt.wantInterrupted {
				t.Errorf("yt-dlp was interrupted = %v, want %v", got, tt.wantInterrupted)
			}
			fake.RequireNoCopy(t)
		})
	}
}

// A run stopped by quitting whose directory then cannot be removed: quitting
// waits for the attempt and reports the directory, since nothing else will.
func TestShutdownReportsADirectoryThatCouldNotBeRemoved(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.HangUnremovable, "")
	t.Cleanup(ytdlp.Reopen)
	cmd, release := command(t, context.Background(), "--", "https://example.invalid/v")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		_ = cmd.Wait()
		released <- release()
	}()
	fake.WaitCopying(t)

	err := ytdlp.Shutdown()
	select {
	case rerr := <-released:
		if rerr == nil {
			t.Error("release reported no error for a directory it could not remove")
		}
	default:
		t.Fatal("Shutdown returned before the attempt to remove the directory was over")
	}
	left := fake.CopiesLeft(t)
	if len(left) != 1 {
		t.Fatalf("left in temporary storage: %q, want the one directory", left)
	}
	if err == nil || !strings.Contains(err.Error(), "could not be removed") || !strings.Contains(err.Error(), left[0]) {
		t.Errorf("Shutdown = %v, want it to report that %s could not be removed", err, left[0])
	}

	// Once it is gone, it is not reported again.
	fake.Unlock(t)
	if err := release(); err != nil {
		t.Fatalf("release once the directory can be emptied: %v", err)
	}
	if err := ytdlp.Shutdown(); err != nil {
		t.Errorf("Shutdown after the directory was removed: %v", err)
	}
}

// A caller that never comes back does not hold up quitting for ever, and
// Shutdown says what it left.
func TestShutdownGivesUpOnARunThatIsNeverReleased(t *testing.T) {
	ytdlptest.InstallCopier(t, ytdlptest.Finish, "")
	t.Cleanup(ytdlp.Reopen)
	_, release := command(t, context.Background(), "--version")
	t.Cleanup(func() { _ = release() })

	start := time.Now()
	err := ytdlp.Shutdown()
	if took := time.Since(start); took > ytdlp.Grace+4*time.Second {
		t.Errorf("Shutdown took %v", took)
	}
	if err == nil || !strings.Contains(err.Error(), "1 run(s)") {
		t.Errorf("Shutdown = %v, want it to report the one run left", err)
	}
}

func TestNoRunIsMadeAfterShutdown(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, "")
	t.Cleanup(ytdlp.Reopen)
	if err := ytdlp.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	cmd, release, err := ytdlp.Command(context.Background(), "--version")
	if !errors.Is(err, ytdlp.ErrShutdown) || cmd != nil || release != nil {
		t.Errorf("Command after Shutdown = %v, release set = %v, %v; want ErrShutdown alone", cmd, release != nil, err)
	}
	if left := fake.CopiesLeft(t); len(left) != 0 {
		t.Errorf("left in temporary storage: %q", left)
	}
	ytdlp.Reopen()
	if _, release, err := ytdlp.Command(context.Background(), "--version"); err != nil {
		t.Errorf("Command after Reopen: %v", err)
	} else if err := release(); err != nil {
		t.Errorf("release: %v", err)
	}
}
