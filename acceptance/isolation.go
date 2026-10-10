package acceptance

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// HostNetNSEnv tells a case which network namespace is the host's, so that
// it can check for itself that it is not running in it.
var HostNetNSEnv = strings.ToUpper(appdir.Name) + "_ACCEPTANCE_HOST_NETNS"

// Isolation runs the deterministic cases with no network at all, enforced by
// the kernel: each run gets a new, empty network namespace (Linux, through
// unshare(1) in an unprivileged user namespace), which has a loopback
// interface that is down and nothing else. Proxy variables, DNS settings and
// good intentions are not isolation; this is.
//
// It is established and then verified, never assumed: the process that runs
// the cases records the namespace it is in and the interfaces it can see,
// and Verify reads that record. Where it cannot be established (another
// operating system, no unshare, user namespaces disabled), the cases that
// need it are blocked. They are never run without it and called passed.
type Isolation struct {
	hostNS string
	dir    string // where each run's proof is written
}

// NewIsolation prepares network isolation, proving first, with a run that
// does nothing else, that this machine can establish it.
func NewIsolation(dir string) (*Isolation, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("kernel network isolation is implemented for Linux only, and this is %s", runtime.GOOS)
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		return nil, errors.New("unshare(1) is not installed: no network namespace can be created")
	}
	host, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return nil, fmt.Errorf("read this process's network namespace: %w", err)
	}
	iso := &Isolation{hostNS: host, dir: dir}
	cmd, proof, err := iso.Command("true")
	if err != nil {
		return nil, err
	}
	defer os.Remove(proof)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("a network namespace could not be created (are unprivileged user namespaces enabled?): %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := iso.Verify(proof); err != nil {
		return nil, err
	}
	return iso, nil
}

// Env is what the isolated cases are told: which namespace is the host's.
func (i *Isolation) Env() []string { return []string{HostNetNSEnv + "=" + i.hostNS} }

// Command returns argv as a command that runs in a new network namespace,
// and the file that will hold its proof: before it becomes argv, the process
// writes down the namespace it is in and the interfaces that namespace has.
// The same process then executes argv, so the proof is of the very process
// tree that does the work.
func (i *Isolation) Command(argv ...string) (*exec.Cmd, string, error) {
	f, err := os.CreateTemp(i.dir, "netns-proof")
	if err != nil {
		return nil, "", err
	}
	f.Close()
	const record = `readlink /proc/self/ns/net > "$0" && cat /proc/net/dev >> "$0" && exec "$@"`
	args := append([]string{"--user", "--map-current-user", "--net", "--", "/bin/sh", "-c", record, f.Name()}, argv...)
	return exec.Command("unshare", args...), f.Name(), nil
}

// Verify checks a run's proof: it ran in a network namespace that is not
// the host's, and that namespace has no interface but loopback.
func (i *Isolation) Verify(proof string) error {
	data, err := os.ReadFile(proof)
	if err != nil {
		return fmt.Errorf("no proof of network isolation: %w", err)
	}
	ns, devices, _ := strings.Cut(string(data), "\n")
	return checkIsolated(strings.TrimSpace(ns), i.hostNS, devices)
}

// checkIsolated judges a namespace and its /proc/net/dev listing.
func checkIsolated(ns, hostNS, procNetDev string) error {
	switch {
	case !strings.HasPrefix(ns, "net:["):
		return fmt.Errorf("no network namespace was recorded (got %q)", ns)
	case hostNS == "" || ns == hostNS:
		return fmt.Errorf("the cases ran in the host's network namespace (%s): the network was not isolated", ns)
	}
	interfaces := interfaceNames(procNetDev)
	if len(interfaces) == 0 {
		return errors.New("the network namespace's interfaces could not be read")
	}
	for _, name := range interfaces {
		if name != "lo" {
			return fmt.Errorf("the network namespace has the interface %s: only loopback may exist", name)
		}
	}
	return nil
}

// interfaceNames reads the interface names out of /proc/net/dev's text.
func interfaceNames(procNetDev string) []string {
	var names []string
	sc := bufio.NewScanner(strings.NewReader(procNetDev))
	for sc.Scan() {
		if name, _, ok := strings.Cut(sc.Text(), ":"); ok {
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names
}

// VerifyThisProcessIsIsolated is the check a case makes for itself: this
// process is in a network namespace other than the host's (hostNS, from
// HostNetNSEnv), it has no interface but loopback, and an attempt to reach
// an outside address fails at once because there is no route to anywhere.
// The attempt is made only after the first two hold, so nothing leaves the
// machine when they do not.
func VerifyThisProcessIsIsolated(hostNS string) error {
	if hostNS == "" {
		return errors.New("not started by the acceptance runner: no network isolation was established")
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return fmt.Errorf("read this process's network namespace: %w", err)
	}
	devices, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return fmt.Errorf("read this process's network interfaces: %w", err)
	}
	if err := checkIsolated(ns, hostNS, string(devices)); err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", "192.0.2.1:9", 2*time.Second) // TEST-NET-1: nobody's address
	if err == nil {
		conn.Close()
		return errors.New("an outside address answered: the network is not isolated")
	}
	if !errors.Is(err, syscall.ENETUNREACH) {
		return fmt.Errorf("reaching an outside address failed with %v, not \"network is unreachable\": the network may not be isolated", err)
	}
	return nil
}

// SameNetworkNamespace reports whether the process pid is in this process's
// network namespace: a case uses it to check that the player it started is
// inside the isolation with it.
func SameNetworkNamespace(pid int) error {
	mine, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return err
	}
	theirs, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/net", pid))
	if err != nil {
		return fmt.Errorf("read the network namespace of process %d: %w", pid, err)
	}
	if mine != theirs {
		return fmt.Errorf("process %d is in the network namespace %s, outside the isolated %s", pid, theirs, mine)
	}
	return nil
}
