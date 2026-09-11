//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

const terminalDifferentControllingProbe = "WTGC_SELECTION_TERMINAL_DIFFERENT_CONTROLLING_PROBE"

const terminalGetPTYNumber = 0x80045430 // TIOCGPTN
const terminalUnlockPTY = 0x40045431    // TIOCSPTLCK

func TestTerminalSelectionInputUsesSuppliedTerminalInsteadOfControllingTerminal(t *testing.T) {
	switch os.Getenv(terminalDifferentControllingProbe) {
	case "child":
		supplied := os.NewFile(3, "supplied-terminal")
		master := os.NewFile(4, "supplied-terminal-master")
		if supplied == nil || master == nil {
			t.Fatal("missing supplied terminal descriptors")
		}
		defer supplied.Close()
		defer master.Close()
		controlling, err := os.Open("/dev/tty")
		if err != nil {
			t.Fatalf("open controlling terminal: %v", err)
		}
		defer controlling.Close()
		if err := terminalFilesDiffer(supplied, controlling); err != nil {
			t.Fatalf("supplied and controlling terminals were not distinct: %v", err)
		}
		reader, release, err := prepareSelectionInput(supplied)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		owned, ok := reader.(*terminalSelectionInput)
		if !ok {
			t.Fatalf("prepared terminal input type=%T", reader)
		}
		if err := sameTerminal(supplied, owned.file); err != nil {
			t.Fatalf("prepared input did not reopen supplied terminal: %v", err)
		}
		if err := terminalFilesDiffer(controlling, owned.file); err != nil {
			t.Fatalf("prepared input used controlling terminal: %v", err)
		}
		if _, err := master.Write([]byte("x\n")); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 2)
		count, err := reader.Read(buffer)
		if err != nil || string(buffer[:count]) != "x\n" {
			t.Fatalf("supplied terminal read count=%d data=%q err=%v", count, buffer[:count], err)
		}
		return
	case "parent":
		master, supplied := openDistinctTerminal(t)
		defer master.Close()
		defer supplied.Close()
		command := exec.Command(os.Args[0], "-test.run=^TestTerminalSelectionInputUsesSuppliedTerminalInsteadOfControllingTerminal$")
		command.Env = append(os.Environ(), terminalDifferentControllingProbe+"=child")
		command.Stdin = os.Stdin
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		command.ExtraFiles = []*os.File{supplied, master}
		if err := command.Run(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is unavailable; no native PTY launcher")
	}
	command := "env " + terminalDifferentControllingProbe + "=parent " + quoteSelectionProbe(os.Args[0]) + " -test.run=^TestTerminalSelectionInputUsesSuppliedTerminalInsteadOfControllingTerminal$"
	output, err := exec.Command("script", "-q", "-e", "-c", command, "/dev/null").CombinedOutput()
	if err != nil {
		t.Fatalf("PTY different-terminal probe: %v output=%q", err, output)
	}
}

func openDistinctTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	number, err := terminalPTYNumber(master)
	if err != nil {
		master.Close()
		t.Fatal(err)
	}
	if err := unlockTerminalPTY(master); err != nil {
		master.Close()
		t.Fatal(err)
	}
	supplied, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		master.Close()
		t.Fatal(err)
	}
	return master, supplied
}

func terminalPTYNumber(file *os.File) (uint32, error) {
	var number uint32
	if err := terminalIOCTL(file, terminalGetPTYNumber, reflect.ValueOf(&number).Pointer()); err != nil {
		return 0, err
	}
	return number, nil
}

func unlockTerminalPTY(file *os.File) error {
	locked := uint32(0)
	return terminalIOCTL(file, terminalUnlockPTY, reflect.ValueOf(&locked).Pointer())
}

func terminalIOCTL(file *os.File, operation uintptr, value uintptr) error {
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	controlErr := raw.Control(func(fd uintptr) {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, operation, value)
		if errno != 0 {
			ioctlErr = errno
		}
	})
	return errors.Join(controlErr, ioctlErr)
}

func terminalFilesDiffer(first, second *os.File) error {
	firstInfo, err := first.Stat()
	if err != nil {
		return err
	}
	secondInfo, err := second.Stat()
	if err != nil {
		return err
	}
	if os.SameFile(firstInfo, secondInfo) {
		return fmt.Errorf("terminal files are the same")
	}
	return nil
}

func TestTerminalPTYTopologyHelpersRejectClosedDescriptor(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := terminalPTYNumber(file); err == nil || !strings.Contains(err.Error(), "file already closed") {
		t.Fatalf("closed terminal descriptor err=%v", err)
	}
}
