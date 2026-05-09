// mcp_modal.go renders the MCP setup modal showing server status, bearer token,
// and pre-filled connection commands. Opened via the `mcp` command.
package tui

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

const mcpBoxW = 94
const mcpBoxH = 38
const mcpTokenRevealDuration = 10 * time.Second

// handleMcpKey processes key events while the MCP modal is open.
func (t *TUI) handleMcpKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.mcpM.active = false
		return false
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q', '`':
			t.mcpM.active = false
			return false
		case 'x':
			t.mcpM.tokenRevealed = !t.mcpM.tokenRevealed
			if t.mcpM.tokenRevealed {
				t.mcpM.revealExpiry = time.Now().Add(mcpTokenRevealDuration)
			}
			return false
		case 'c':
			t.mcpCopyToken()
			return false
		}
	}
	return false
}

// renderMcpModal draws the MCP setup modal.
func (t *TUI) renderMcpModal() {
	// Auto-hide token on render tick.
	if t.mcpM.tokenRevealed && time.Now().After(t.mcpM.revealExpiry) {
		t.mcpM.tokenRevealed = false
	}

	s := t.screen
	sw, sh := s.Size()

	boxW := mcpBoxW
	if sw-4 < boxW {
		boxW = sw - 4
	}
	if boxW < 30 {
		boxW = 30
	}
	boxH := mcpBoxH
	if sh-4 < boxH {
		boxH = sh - 4
	}
	if boxH < 12 {
		boxH = 12
	}

	startX := (sw - boxW) / 2
	startY := (sh - boxH) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	bgStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	borderStyle := bgStyle.Foreground(tcell.ColorGray)
	titleStyle := bgStyle.Foreground(tcell.ColorDodgerBlue).Bold(true)
	labelStyle := bgStyle.Foreground(tcell.ColorWhite).Bold(true)
	valueStyle := bgStyle.Foreground(tcell.ColorSilver)
	greenStyle := bgStyle.Foreground(tcell.ColorGreen)
	dimStyle := bgStyle.Foreground(tcell.ColorGray)
	codeStyle := bgStyle.Foreground(tcell.ColorYellow)
	helpStyle := bgStyle.Foreground(tcell.ColorGray)

	// Draw opaque background.
	for y := startY; y < startY+boxH && y < sh; y++ {
		for x := startX; x < startX+boxW && x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bgStyle)
		}
	}

	// Draw border.
	s.SetContent(startX, startY, '\u250c', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY, '\u2510', nil, borderStyle)
	s.SetContent(startX, startY+boxH-1, '\u2514', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY+boxH-1, '\u2518', nil, borderStyle)
	for x := startX + 1; x < startX+boxW-1 && x < sw; x++ {
		s.SetContent(x, startY, '\u2500', nil, borderStyle)
		s.SetContent(x, startY+boxH-1, '\u2500', nil, borderStyle)
	}
	for y := startY + 1; y < startY+boxH-1 && y < sh; y++ {
		s.SetContent(startX, y, '\u2502', nil, borderStyle)
		s.SetContent(startX+boxW-1, y, '\u2502', nil, borderStyle)
	}

	innerW := boxW - 2
	innerX := startX + 1

	drawLine := func(y int, text string, style tcell.Style) {
		for i, ch := range text {
			if i >= innerW {
				break
			}
			s.SetContent(innerX+i, y, ch, nil, style)
		}
	}

	// Title centered in top border.
	title := " initech mcp "
	titleStart := startX + (boxW-len([]rune(title)))/2
	for i, ch := range title {
		if titleStart+i < startX+boxW-1 {
			s.SetContent(titleStart+i, startY, ch, nil, titleStyle)
		}
	}

	iy := startY + 2 // skip border + blank line

	disabled := t.mcpPort == 0

	// Status line.
	if disabled {
		drawLine(iy, " Status     disabled (set mcp_port in initech.yaml to enable)", dimStyle)
	} else {
		statusText := fmt.Sprintf(" Status     running on %s:%d", t.mcpBind, t.mcpPort)
		drawLine(iy, statusText, greenStyle)
	}
	iy += 2

	if disabled {
		// No more content when disabled.
		helpY := startY + boxH - 2
		drawLine(helpY, " [Esc] close", helpStyle)
		return
	}

	// Token line.
	drawLine(iy, " Token      ", labelStyle)
	tokenDisplay := strings.Repeat("\u25cf", 24) + "  [x] reveal"
	tokenStyle := dimStyle
	if t.mcpM.tokenRevealed {
		tokenDisplay = t.mcpToken + "  [x] hide"
		tokenStyle = valueStyle
	}
	// Draw token value after label.
	col := innerX + 12
	for _, ch := range tokenDisplay {
		if col >= innerX+innerW {
			break
		}
		s.SetContent(col, iy, ch, nil, tokenStyle)
		col++
	}
	iy += 2

	// Connection commands.
	host := mcpLANIP()
	if t.mcpBind == "127.0.0.1" {
		host = "localhost"
	}

	drawLine(iy, " Connect", labelStyle)
	iy++
	// Separator.
	for x := innerX + 1; x < innerX+innerW-1; x++ {
		s.SetContent(x, iy, '\u2500', nil, borderStyle)
	}
	iy++

	// Claude Code command.
	drawLine(iy, " Claude Code:", labelStyle)
	iy++
	claudeCmd := fmt.Sprintf("   claude mcp add --transport http \\")
	drawLine(iy, claudeCmd, codeStyle)
	iy++
	claudeCmd2 := fmt.Sprintf("     --header 'Authorization: Bearer %s' \\", t.mcpToken)
	drawLine(iy, claudeCmd2, codeStyle)
	iy++
	claudeCmd3 := fmt.Sprintf("     initech http://%s:%d/mcp", host, t.mcpPort)
	drawLine(iy, claudeCmd3, codeStyle)
	iy += 2

	// curl command.
	drawLine(iy, " curl:", labelStyle)
	iy++
	curlCmd := fmt.Sprintf("   curl -X POST http://%s:%d/mcp \\", host, t.mcpPort)
	drawLine(iy, curlCmd, codeStyle)
	iy++
	curlCmd2 := fmt.Sprintf("     -H 'Authorization: Bearer %s' \\", t.mcpToken)
	drawLine(iy, curlCmd2, codeStyle)
	iy++
	curlCmd3 := "     -H 'Content-Type: application/json' \\"
	drawLine(iy, curlCmd3, codeStyle)
	iy++
	curlCmd4 := `     -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`
	drawLine(iy, curlCmd4, codeStyle)
	iy += 2

	// Plugin env var.
	drawLine(iy, " Plugin env var:", labelStyle)
	iy++
	envLine := fmt.Sprintf("   export INITECH_MCP_TOKEN=%s", t.mcpToken)
	drawLine(iy, envLine, codeStyle)

	// Help line (last interior row).
	helpY := startY + boxH - 2
	help := " [c] copy token   [x] toggle token   [Esc] close"
	drawLine(helpY, help, helpStyle)
}

// mcpCopyToken copies the bearer token to clipboard via OSC 52 escape
// sequence (works in most modern terminals).
func (t *TUI) mcpCopyToken() {
	if t.mcpToken == "" {
		return
	}
	// Write directly to the terminal (stdout, not the screen).
	fmt.Print(buildOSC52(t.mcpToken))
}

// buildOSC52 returns the OSC 52 clipboard-set escape sequence for content.
// Format: \033]52;c;<base64>\a — recognised by most modern terminals as a
// "set system clipboard to this base64-decoded payload" request.
//
// Extracted so ini-jr0 Phase 3 can verify the emitted sequence round-trips
// without capturing stdout. Used by both mcpCopyToken and webCopyURL so a
// future encoding change happens in one place.
func buildOSC52(content string) string {
	return fmt.Sprintf("\033]52;c;%s\a", base64Encode(content))
}

// base64Encode returns the standard base64 encoding of s.
func base64Encode(s string) string {
	const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out []byte
	b := []byte(s)
	for i := 0; i < len(b); i += 3 {
		var n uint32
		remaining := len(b) - i
		switch {
		case remaining >= 3:
			n = uint32(b[i])<<16 | uint32(b[i+1])<<8 | uint32(b[i+2])
			out = append(out, b64[n>>18&0x3F], b64[n>>12&0x3F], b64[n>>6&0x3F], b64[n&0x3F])
		case remaining == 2:
			n = uint32(b[i])<<16 | uint32(b[i+1])<<8
			out = append(out, b64[n>>18&0x3F], b64[n>>12&0x3F], b64[n>>6&0x3F], '=')
		case remaining == 1:
			n = uint32(b[i]) << 16
			out = append(out, b64[n>>18&0x3F], b64[n>>12&0x3F], '=', '=')
		}
	}
	return string(out)
}

// mcpLANIP returns the first non-loopback IPv4 address for use in connection
// commands. Falls back to "localhost" if no suitable address is found.
func mcpLANIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "localhost"
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip.IsLoopback() || ip.To4() == nil {
			continue
		}
		return ip.String()
	}
	return "localhost"
}
