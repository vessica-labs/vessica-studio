package cloudauth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
)

type rotatingAPI struct {
	fakeAPI
	mu      sync.Mutex
	current string
	count   int
}

func (a *rotatingAPI) Refresh(_ context.Context, token string) (cloud.Token, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if token != a.current {
		return cloud.Token{}, errors.New("refresh replay")
	}
	time.Sleep(time.Millisecond)
	a.count++
	a.current = fmt.Sprintf("rotation-%d", a.count)
	return cloud.Token{AccessToken: "memory-only", RefreshToken: a.current, ExpiresIn: 60}, nil
}

func TestConcurrentManagersSerializeRotation(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Save(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	api := &rotatingAPI{current: secret}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := New(api, store).Token(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if api.count != 12 {
		t.Fatalf("rotations = %d", api.count)
	}
}

func TestCredentialLockProcessHelper(t *testing.T) {
	path := os.Getenv("VSTD_TEST_LOCK_PATH")
	if path == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	unlock, err := acquireCredentialFileLock(ctx, path)
	if os.Getenv("VSTD_TEST_LOCK_MODE") == "blocked" {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected blocked lock: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	fmt.Println("locked")
	if os.Getenv("VSTD_TEST_LOCK_MODE") == "hold" {
		time.Sleep(30 * time.Second)
	}
}

func TestCredentialLockAcrossProcessesAndCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account.lock")
	child := func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCredentialLockProcessHelper$")
		cmd.Env = append(os.Environ(), "VSTD_TEST_LOCK_PATH="+path, "VSTD_TEST_LOCK_MODE="+mode)
		return cmd
	}
	unlock, err := acquireCredentialFileLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if output, err := child("blocked").CombinedOutput(); err != nil {
		t.Fatalf("child: %v: %s", err, output)
	}
	unlock()
	cmd := child("hold")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("child lock: %q %v", line, err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := acquireCredentialFileLock(ctx, path)
	if err != nil {
		t.Fatalf("crash left lock held: %v", err)
	}
	release()
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatal("lock must contain no credential data")
	}
}
