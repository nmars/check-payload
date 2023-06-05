package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"

	corev1 "k8s.io/api/core/v1"
)

var (
	ErrNotGolangExe = errors.New("not golang executable")
)

type ValidationFn func(ctx context.Context, container *corev1.Container, path string) error

var validationFns = map[string][]ValidationFn{
	"go": {
		validateGoSymbols,
		validateGoVersion,
	},
	"exe": {
		validateExe,
	},
	"all": {},
}

func validateGoSymbols(ctx context.Context, container *corev1.Container, path string) error {
	symtable, err := readTable(path)
	if err != nil {
		return fmt.Errorf("expected symbols not found for %v: %v", filepath.Base(path), err)
	}
	if err := ExpectedSyms(requiredGolangSymbols, symtable); err != nil {
		return fmt.Errorf("expected symbols not found for %v: %v", filepath.Base(path), err)
	}
	return nil
}

func validateGoVersion(ctx context.Context, container *corev1.Container, path string) error {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "version", "-m", path)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return err
	}

	if !bytes.Contains(stdout.Bytes(), []byte("CGO_ENABLED")) || !bytes.Contains(stdout.Bytes(), []byte("ldflags")) {
		return fmt.Errorf("binary is not CGO_ENABLED or static with ldflags")
	}

	return nil
}

func validateExe(ctx context.Context, container *corev1.Container, path string) error {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "readelf", "-d", path)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return err
	}

	if !bytes.Contains(stdout.Bytes(), []byte("Shared library: [libdl")) {
		return fmt.Errorf("binary is not dynamic executable with libdl")
	}

	return nil
}

func isGoExecutable(ctx context.Context, path string) error {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "version", path)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return err
	}

	goVersionRegex := regexp.MustCompile(`.*:\s+go.*`)
	if isGo := goVersionRegex.Match(stdout.Bytes()); isGo {
		return nil
	}

	return ErrNotGolangExe
}

func scanBinary(ctx context.Context, pod *corev1.Pod, container *corev1.Container, path string) *ScanResult {
	var allFn = validationFns["all"]
	var goFn = validationFns["go"]
	var exeFn = validationFns["exe"]

	for _, fn := range allFn {
		if err := fn(ctx, container, path); err != nil {
			return NewScanResult().SetBinaryPath(path).SetError(err)
		}
	}

	// is this a go executable
	if isGoExecutable(ctx, path) == nil {
		for _, fn := range goFn {
			if err := fn(ctx, container, path); err != nil {
				return NewScanResultByPod(pod).SetBinaryPath(path).SetError(err)
			}
		}
	} else {
		// is a regular binary
		for _, fn := range exeFn {
			if err := fn(ctx, container, path); err != nil {
				return NewScanResultByPod(pod).SetBinaryPath(path).SetError(err)
			}
		}
	}

	return NewScanResultByPod(pod).SetBinaryPath(path).Success()
}
