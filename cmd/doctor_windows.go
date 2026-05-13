//go:build windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// checkConfigPermissions is a no-op on Windows. NTFS uses ACLs rather than
// Unix mode bits, and Go's os.Stat reports a synthetic mode that doesn't
// reflect the ACL state. Translating ACL audit ("is this file readable by
// any principal other than the owner?") is a separate scope — skipping
// the check here matches the cleaner choice the bead recommended.
func checkConfigPermissions(cfgPath string) []checkResult {
	return nil
}

// platformEnvironmentChecks returns Windows-specific diagnostics: ConPTY
// capability, terminal emulator detection, and PATH validation.
func platformEnvironmentChecks() []checkResult {
	var results []checkResult

	results = append(results, checkConPTY()...)
	results = append(results, checkWindowsTerminal())
	results = append(results, checkClaudeInstallWindows()...)

	return results
}

// checkConPTY verifies that the Windows version supports ConPTY (Windows 10
// 1809 / build 17763+). Older versions will fail at PTY creation.
func checkConPTY() []checkResult {
	major, minor, build := windowsVersion()
	verStr := fmt.Sprintf("%d.%d.%d", major, minor, build)

	if major < 10 {
		return []checkResult{{
			Label:  "ConPTY",
			Status: "FAIL",
			Detail: fmt.Sprintf("Windows %s — ConPTY requires Windows 10 1809+ (build 17763)", verStr),
		}}
	}

	if build < 17763 {
		return []checkResult{{
			Label:  "ConPTY",
			Status: "FAIL",
			Detail: fmt.Sprintf("Windows build %d — ConPTY requires build 17763+ (version 1809)", build),
		}}
	}

	// Verify the ConPTY API is actually loadable.
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	createPseudo := kernel32.NewProc("CreatePseudoConsole")
	if err := createPseudo.Find(); err != nil {
		return []checkResult{{
			Label:  "ConPTY",
			Status: "FAIL",
			Detail: fmt.Sprintf("Windows %s but CreatePseudoConsole not found in kernel32.dll", verStr),
		}}
	}

	return []checkResult{{
		Label:  "ConPTY",
		Status: "OK",
		Detail: fmt.Sprintf("Windows %s — CreatePseudoConsole available", verStr),
	}}
}

// checkWindowsTerminal detects the terminal emulator. Windows Terminal sets
// WT_SESSION; cmd.exe and legacy PowerShell have limited ANSI support.
func checkWindowsTerminal() checkResult {
	if wt := os.Getenv("WT_SESSION"); wt != "" {
		return checkResult{
			Label:  "Terminal",
			Status: "OK",
			Detail: "Windows Terminal detected (WT_SESSION set)",
		}
	}

	if wezterm := os.Getenv("WEZTERM_EXECUTABLE"); wezterm != "" {
		return checkResult{
			Label:  "Terminal",
			Status: "OK",
			Detail: "WezTerm detected",
		}
	}

	return checkResult{
		Label:  "Terminal",
		Status: "WARN",
		Detail: "Windows Terminal not detected. cmd.exe/legacy PowerShell have limited ANSI support. Install Windows Terminal for best experience",
	}
}

// checkClaudeInstallWindows checks Claude Code installation paths specific to
// Windows (npm global, scoop, winget, %LOCALAPPDATA%).
func checkClaudeInstallWindows() []checkResult {
	// LookPath check is already done by the prereq system. This adds
	// Windows-specific hints for common install locations when claude is missing.
	if _, err := exec.LookPath("claude"); err == nil {
		return nil
	}

	var hints []string

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData != "" {
		claudePath := localAppData + `\Programs\claude-code\claude.exe`
		if _, err := os.Stat(claudePath); err == nil {
			hints = append(hints, fmt.Sprintf("found at %s but not on PATH", claudePath))
		}
	}

	// Check npm global.
	if out, err := exec.Command("npm", "root", "-g").Output(); err == nil {
		npmRoot := strings.TrimSpace(string(out))
		claudeNpm := npmRoot + `\@anthropic-ai\claude-code\cli.js`
		if _, err := os.Stat(claudeNpm); err == nil {
			hints = append(hints, "installed via npm global but 'claude' not on PATH")
		}
	}

	if len(hints) == 0 {
		return nil
	}

	return []checkResult{{
		Label:  "Claude (Windows)",
		Status: "WARN",
		Detail: strings.Join(hints, "; ") + ". Add to PATH or reinstall with: npm install -g @anthropic-ai/claude-code",
	}}
}

// windowsVersion returns the major, minor, and build number of the running
// Windows version. Uses RtlGetVersion which reports the true version even
// when the app lacks a compatibility manifest (unlike GetVersionEx).
func windowsVersion() (major, minor, build uint32) {
	type rtlOSVersionInfo struct {
		OSVersionInfoSize uint32
		MajorVersion      uint32
		MinorVersion      uint32
		BuildNumber       uint32
		PlatformId        uint32
		CSDVersion        [128]uint16
	}

	ntdll := windows.NewLazySystemDLL("ntdll.dll")
	proc := ntdll.NewProc("RtlGetVersion")
	if err := proc.Find(); err != nil {
		return 0, 0, 0
	}

	var info rtlOSVersionInfo
	info.OSVersionInfoSize = uint32(unsafe.Sizeof(info))
	ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&info)))
	if ret != 0 {
		return 0, 0, 0
	}

	return info.MajorVersion, info.MinorVersion, info.BuildNumber
}
