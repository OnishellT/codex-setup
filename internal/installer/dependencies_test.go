package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRTKBinaryAcceptsOnlyRegularEntry(t *testing.T) {
	var b bytes.Buffer
	gw := gzip.NewWriter(&b)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "rtk", Mode: 0755, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("bin")); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gw.Close()
	got, err := rtkBinary(b.Bytes())
	if err != nil || string(got) != "bin" {
		t.Fatalf("rtkBinary = %q, %v", got, err)
	}
}

func TestDependencyPlanHasNoCommandsWhenEmpty(t *testing.T) {
	e := &Engine{}
	p, err := e.PlanDependencies(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.NeedsInstall() || len(p.Commands) != 0 {
		t.Fatalf("unexpected dependency plan: %+v", p)
	}
}

func TestDependencyVersionProbe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	for _, tc := range []struct {
		out string
		ok  bool
	}{{"tmux 3.2\n", false}, {"tmux 3.3\n", true}, {"garbage\n", false}} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+tc.out+"'\n"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		if got := dependencySatisfied(dependencyRequirement{depTmux, "tmux", "3.3"}); got != tc.ok {
			t.Errorf("%q: got %v", tc.out, got)
		}
	}
}

func TestCertificatesAvailableUsesFreshPEMBundle(t *testing.T) {
	old := dependencyCABundles
	t.Cleanup(func() { dependencyCABundles = old })
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	dependencyCABundles = []string{bundle}
	if certificatesAvailable() {
		t.Fatal("empty bundle accepted")
	}
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if !certificatesAvailable() {
		t.Fatal("valid bundle rejected")
	}
}

func TestPackageCommandsUbuntuAndArch(t *testing.T) {
	oldOS := dependencyOSRelease
	t.Cleanup(func() { dependencyOSRelease = oldOS })
	fakePackageManagers(t)
	for _, tc := range []struct {
		release, family string
		missing         []string
		want            [][]string
	}{{"ID=ubuntu\nVERSION_ID=24.04\n", "apt", []string{"xz"}, [][]string{{"update"}, {"install", "-y", "xz-utils"}}}, {"ID=arch\n", "pacman", []string{"python3"}, [][]string{{"-S", "--needed", "--noconfirm", "python"}}}} {
		release := filepath.Join(t.TempDir(), "os-release")
		if err := os.WriteFile(release, []byte(tc.release), 0600); err != nil {
			t.Fatal(err)
		}
		dependencyOSRelease = release
		m, err := packageManager()
		if err != nil || m.family != tc.family {
			t.Fatalf("manager: %+v %v", m, err)
		}
		cmds, err := dependencyCommands(tc.missing)
		if err != nil {
			t.Fatal(err)
		}
		if len(cmds) != len(tc.want) {
			t.Fatalf("commands: %+v", cmds)
		}
		for i, want := range tc.want {
			assertCommandArgs(t, cmds[i], want)
		}
	}
}

func fakePackageManagers(t *testing.T) string {
	t.Helper()
	old := dependencyManagerPath
	t.Cleanup(func() { dependencyManagerPath = old })
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, name := range []string{commandAptGet, "pacman", "sudo"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	dependencyManagerPath = func(name string) (string, error) { return filepath.Join(dir, name), nil }
	return dir
}

func assertCommandArgs(t *testing.T, command DependencyCommand, want []string) {
	t.Helper()
	args := command.Args
	if filepath.Base(command.Path) == "sudo" {
		args = args[1:]
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %v, want %v", args, want)
		}
	}
}

func TestInstallDependenciesSuccessfulFakeRecheck(t *testing.T) {
	oldOS := dependencyOSRelease
	t.Cleanup(func() { dependencyOSRelease = oldOS })
	release := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(release, []byte("ID=ubuntu\nVERSION_ID=24.04\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dependencyOSRelease = release
	for _, tc := range []struct {
		name             string
		success, creates bool
	}{{"success", true, true}, {"postcheck failure", true, false}, {"command failure", false, false}} {
		t.Run(tc.name, func(t *testing.T) { runFakeDependencyCase(t, tc.success, tc.creates) })
	}
}

func runFakeDependencyCase(t *testing.T, success, creates bool) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	oldManager := dependencyManagerPath
	t.Cleanup(func() { dependencyManagerPath = oldManager })
	dependencyManagerPath = func(name string) (string, error) { return filepath.Join(dir, name), nil }
	python := filepath.Join(dir, "python3")
	body := "#!/bin/sh\n"
	if creates {
		body += "printf '#!/bin/sh\\nexit 0\\n' > " + python + "\n/bin/chmod 755 " + python + "\n"
	}
	if !success {
		body += "exit 1\n"
	}
	if err := os.WriteFile(filepath.Join(dir, commandAptGet), []byte(body+"exit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte("#!/bin/sh\nexec \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Home: t.TempDir(), Modules: []Module{{ID: "context-handoff"}}}
	p, err := e.PlanDependencies([]string{"context-handoff"})
	if err != nil {
		t.Fatal(err)
	}
	err = e.InstallDependencies(p, nil, nil, nil)
	if success && creates && err != nil {
		t.Fatalf("successful install: %v", err)
	}
	if (!success || !creates) && err == nil {
		t.Fatal("expected installation failure")
	}
}

func TestDependencyCommandValidatesInnerManager(t *testing.T) {
	dir := fakePackageManagers(t)
	for _, tc := range []struct {
		path    string
		args    []string
		allowed bool
	}{
		{filepath.Join(dir, "sudo"), []string{filepath.Join(dir, commandAptGet), "update"}, true},
		{filepath.Join(dir, "sudo"), []string{"/writable/apt-get", "update"}, false},
		{filepath.Join(dir, "sudo"), []string{filepath.Join(dir, "sudo"), "sh"}, false},
		{"/writable/sudo", []string{filepath.Join(dir, commandAptGet), "update"}, false},
		{filepath.Join(dir, commandAptGet), []string{"update"}, true},
	} {
		if got := allowlistedDependencyCommand(DependencyCommand{Path: tc.path, Args: tc.args}); got != tc.allowed {
			t.Errorf("%+v allowed=%v", tc, got)
		}
	}
}

func TestSecureManagerIgnoresPATH(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, commandAptGet), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	path, err := secureManagerPath(commandAptGet)
	if err == nil && path != "/usr/bin/apt-get" {
		t.Fatalf("accepted PATH manager %q", path)
	}
	if err := rootOwnedPath(dir); err == nil {
		t.Fatal("user-writable path trusted")
	}
	if _, err := secureManagerPath("../bin/sh"); err == nil {
		t.Fatal("accepted arbitrary executable")
	}
}

func TestSupportedDistroVersions(t *testing.T) {
	for _, tc := range []struct {
		id, version string
		ok          bool
	}{
		{"ubuntu", "22.04", false}, {"ubuntu", "24.04", true}, {"ubuntu", "24.03", false},
		{"debian", "9", false}, {"debian", "12", true}, {"debian", "100", true},
		{"fedora", "39", false}, {"fedora", "40", true}, {"arch", "", true},
		{"ubuntu", "rolling", false}, {"debian", "", false}, {"linuxmint", "24.04", false},
	} {
		if got := supportedDistroVersion(tc.id, tc.version); got != tc.ok {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}

func TestBasePythonDoesNotRequireCurses(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$2\" in *curses*) exit 1;; *) exit 0;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if !dependencySatisfied(dependencyRequirement{depPython, "python3", "3.11"}) {
		t.Fatal("base Python required panel modules")
	}
	if dependencySatisfied(dependencyRequirement{depPythonModules, "python3", "3.11"}) {
		t.Fatal("panel Python accepted missing curses")
	}
}
