package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	qltyVersion        = "0.644.0"
	qltyMaxArchiveSize = 64 << 20
	qltyMaxBinarySize  = 200 << 20
)

type qltyRelease struct {
	archiveName   string
	archiveSHA256 string
	binarySHA256  string
}

var qltyReleases = map[string]qltyRelease{
	"linux/amd64": {
		archiveName:   "qlty-x86_64-unknown-linux-musl.tar.xz",
		archiveSHA256: "9877dceeabc2d91ee57e94eeec818de0b665b97d288af54015e024c7a9c70353",
		binarySHA256:  "ce4b7c117a457a9f70332c267bc2f8e06cccb82a09390c95898a3f1fe7a2ffe3",
	},
	"linux/arm64": {
		archiveName:   "qlty-aarch64-unknown-linux-musl.tar.xz",
		archiveSHA256: "3abcb2131d4cd51f62e13c49faf41efd48c7aa2eb0957249a24dfac09bbff6f0",
		binarySHA256:  "c5d306f334c14e4583c0ea0ea6732761630a2f3d9bdc018f9078d4fdfe5dd713",
	},
	"darwin/amd64": {
		archiveName:   "qlty-x86_64-apple-darwin.tar.xz",
		archiveSHA256: "218ccd5d0885495344138a032e6644ef409c2a4c73711216a965d4d910aebf9e",
		binarySHA256:  "4700e64ff152084b513dd59a1e5c485394d374bad1d259de2403b643241e1d7e",
	},
	"darwin/arm64": {
		archiveName:   "qlty-aarch64-apple-darwin.tar.xz",
		archiveSHA256: "b714064748c319bb37be721ebb561e939a4cc4b87d337024b974319a32e9e767",
		binarySHA256:  "14594f045e1aadebe5fb01e1857b2e8d84f62c19afba7ee03469289d3c816641",
	},
}

var (
	qltyReleaseBaseURL = "https://github.com/qltysh/qlty/releases/download/v" + qltyVersion
	qltyPlatform       = func() string { return runtime.GOOS + "/" + runtime.GOARCH }
	qltyHTTPClient     = &http.Client{Timeout: 2 * time.Minute}
	qltyExtract        = extractQlty
)

func (e *Engine) planQltyInstall(target string, get func(string) (*Change, error), put func(string, []byte, os.FileMode) error) error {
	release, ok := qltyReleases[qltyPlatform()]
	if !ok {
		return fmt.Errorf("Qlty %s no está disponible para %s", qltyVersion, qltyPlatform())
	}
	current, err := get(target)
	if err != nil {
		return err
	}
	if current.existed && sha256Hex(current.original) == release.binarySHA256 {
		if current.mode == 0755 {
			return nil
		}
		return put(target, current.original, 0755)
	}
	archive, err := downloadQlty(release)
	if err != nil {
		return err
	}
	binary, err := qltyExtract(archive, release)
	if err != nil {
		return err
	}
	if sha256Hex(binary) != release.binarySHA256 {
		return errors.New("Qlty descargado: el binario extraído no coincide con el checksum esperado")
	}
	return put(target, binary, 0755)
}

func downloadQlty(release qltyRelease) ([]byte, error) {
	url := qltyReleaseBaseURL + "/" + release.archiveName
	response, err := qltyHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("no se pudo descargar Qlty %s: %w", qltyVersion, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("descarga de Qlty %s respondió %s", qltyVersion, response.Status)
	}
	archive, err := io.ReadAll(io.LimitReader(response.Body, qltyMaxArchiveSize+1))
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la descarga de Qlty: %w", err)
	}
	if len(archive) > qltyMaxArchiveSize {
		return nil, fmt.Errorf("la descarga de Qlty supera el límite de %d MiB", qltyMaxArchiveSize>>20)
	}
	if sha256Hex(archive) != release.archiveSHA256 {
		return nil, errors.New("Qlty descargado: el archivo no coincide con el checksum esperado")
	}
	return archive, nil
}

func extractQlty(archive []byte, release qltyRelease) ([]byte, error) {
	if _, err := exec.LookPath("tar"); err != nil {
		return nil, errors.New("no se encontró tar con soporte xz para extraer Qlty")
	}
	input, err := os.CreateTemp("", "codex-setup-qlty-*.tar.xz")
	if err != nil {
		return nil, err
	}
	inputName := input.Name()
	defer os.Remove(inputName)
	if _, err = input.Write(archive); err == nil {
		err = input.Close()
	} else {
		input.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("no se pudo preparar Qlty para extraer: %w", err)
	}

	output, err := os.CreateTemp("", "codex-setup-qlty-bin-*")
	if err != nil {
		return nil, err
	}
	outputName := output.Name()
	defer os.Remove(outputName)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	stderr := &limitedBuffer{limit: 64 << 10}
	command := exec.CommandContext(ctx, "tar", "-xJOf", inputName, strings.TrimSuffix(release.archiveName, ".tar.xz")+"/qlty")
	command.Stdout = &limitedWriter{writer: output, limit: qltyMaxBinarySize}
	command.Stderr = stderr
	err = command.Run()
	closeErr := output.Close()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("la extracción de Qlty excedió el tiempo límite: %w", ctx.Err())
		}
		return nil, fmt.Errorf("no se pudo extraer Qlty con tar: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if closeErr != nil {
		return nil, closeErr
	}
	binary, err := os.ReadFile(outputName)
	if err != nil {
		return nil, err
	}
	if len(binary) == 0 || len(binary) > qltyMaxBinarySize {
		return nil, errors.New("Qlty extraído tiene un tamaño inválido")
	}
	return binary, nil
}

type limitedWriter struct {
	writer io.Writer
	limit  int
	wrote  int
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	remaining := w.limit - w.wrote
	if remaining <= 0 {
		return 0, errors.New("límite de extracción excedido")
	}
	if len(data) > remaining {
		n, err := w.writer.Write(data[:remaining])
		w.wrote += n
		if err != nil {
			return n, err
		}
		return n, errors.New("límite de extracción excedido")
	}
	n, err := w.writer.Write(data)
	w.wrote += n
	return n, err
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		return len(data), nil
	}
	if len(data) > remaining {
		b.Buffer.Write(data[:remaining])
		return len(data), nil
	}
	return b.Buffer.Write(data)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
