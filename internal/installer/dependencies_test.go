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
	oldManager := dependencyManagerPath
	t.Cleanup(func() { dependencyManagerPath = oldManager })
	for _, name := range []string{"apt-get", "pacman", "sudo"} {
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
	if err := os.WriteFile(filepath.Join(dir, "apt-get"), []byte(body+"exit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte("#!/bin/sh\nexec \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	dependencyManagerPath = func(name string) (string, error) { return filepath.Join(dir, name), nil }
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
