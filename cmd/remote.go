package cmd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Add or remove a remote machine while initech runs",
	Long: `A remote is another machine running initech serve, named under remotes: in
initech.yaml. Window 1 re-reads that block whenever the file changes, so a
remote added there connects, gets its roles and shows its agents without a
restart, and one removed detaches: its agents keep running on that machine, and
any mail it holds for them stays there. A remote whose addr or token changes is
detached and connected again under the same name.

These commands edit initech.yaml (every other key and comment kept) and then
ask window 1 to re-read it at once. The token is never taken on the command
line: pass --token-file, a file only you can read (mode 0600), or leave it off
to be asked for it with nothing echoed.`,
}

var (
	remoteAddr, remoteTokenFile, remoteTokenFlag string
	remoteRoles                                  []string
)

var remoteAddCmd = &cobra.Command{
	Use:   "add <name> --addr <host:port> [--token-file <file>] [--roles <role,...>]",
	Short: "Add a remote to initech.yaml and connect it",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemoteAdd,
}

var remoteRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a remote from initech.yaml and detach it; nothing on it is stopped",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemoteRemove,
}

func init() {
	remoteAddCmd.Flags().StringVar(&remoteAddr, "addr", "", "host:port where the remote's initech serve listens")
	remoteAddCmd.Flags().StringVar(&remoteTokenFile, "token-file", "", "A file holding the remote's token, readable by you only (0600)")
	remoteAddCmd.Flags().StringSliceVar(&remoteRoles, "roles", nil, "Roles to push to the remote (default: the agents it runs)")
	// Declared only to refuse it with the reason, rather than as an unknown flag.
	remoteAddCmd.Flags().StringVar(&remoteTokenFlag, "token", "", "")
	remoteAddCmd.Flags().MarkHidden("token") //nolint:errcheck // The flag exists.
	remoteCmd.AddCommand(remoteAddCmd, remoteRemoveCmd)
	rootCmd.AddCommand(remoteCmd)
}

// errTokenOnCommandLine is the refusal of --token.
var errTokenOnCommandLine = errors.New("a token is never taken on the command line, where it stays in your shell history and the process list: " +
	"put it in a file only you can read (chmod 600) and pass --token-file, or leave both off to be asked for it; " +
	"rotate the token you typed")

// readRemoteToken reads a token typed at a terminal with nothing echoed. ok
// is false when there is no terminal to ask on. A test replaces it.
var readRemoteToken = func(prompt string) (token string, ok bool, err error) {
	if !stdinIsTerminal() {
		return "", false, nil
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", true, fmt.Errorf("read the token: %w", err)
	}
	return strings.TrimSpace(string(b)), true, nil
}

// readTokenFile reads a token from a file that only its owner can read, once.
func readTokenFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s is not a regular file", path)
	}
	// Windows reports no owner-only bits (every writable file is 0666), so
	// the mode is checked where it means something.
	if perm := fi.Mode().Perm(); runtime.GOOS != "windows" && perm&0o077 != 0 {
		return "", fmt.Errorf("token file %s can be read by others (mode %04o): chmod 600 it first", path, perm)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}

func runRemoteAdd(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	defer func() { remoteAddr, remoteTokenFile, remoteTokenFlag, remoteRoles = "", "", "", nil }()
	if cmd.Flags().Changed("token") {
		return errTokenOnCommandLine
	}
	name := args[0]
	if !config.ValidPeerName(name) || name == tui.WindowOnePeerName {
		return fmt.Errorf("%q is not a remote name: letters, digits and hyphens, and not %s", name, tui.WindowOnePeerName)
	}
	if _, _, err := net.SplitHostPort(remoteAddr); err != nil {
		return fmt.Errorf("--addr must be host:port, like 192.168.1.100:7391")
	}
	out := cmd.OutOrStdout()
	var token string
	if remoteTokenFile != "" {
		t, err := readTokenFile(remoteTokenFile)
		if err != nil {
			return err
		}
		token = t
	} else {
		t, ok, err := readRemoteToken(fmt.Sprintf("Token for %s (nothing is shown; Enter alone uses the project's token): ", name))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(out, "no --token-file and no terminal to ask on: %s uses the project's token\n", name)
		}
		token = t
	}
	cfgPath, err := remoteConfigPath()
	if err != nil {
		return err
	}
	err = config.Update(cfgPath, func(p *config.Project) error {
		if _, ok := p.Remotes[name]; ok {
			return fmt.Errorf("initech.yaml already has a remote named %s: remove it first to change it", name)
		}
		if p.Remotes == nil {
			p.Remotes = map[string]config.Remote{}
		}
		p.Remotes[name] = config.Remote{Addr: remoteAddr, Token: token, Roles: remoteRoles}
		return config.Validate(p)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "added remote %s (%s) to initech.yaml\n", name, remoteAddr)
	signalRemotesReload(out, name)
	return nil
}

func runRemoteRemove(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	name := args[0]
	cfgPath, err := remoteConfigPath()
	if err != nil {
		return err
	}
	err = config.Update(cfgPath, func(p *config.Project) error {
		if _, ok := p.Remotes[name]; !ok {
			names := make([]string, 0, len(p.Remotes))
			for n := range p.Remotes {
				names = append(names, n)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return fmt.Errorf("initech.yaml has no remotes")
			}
			return fmt.Errorf("initech.yaml has no remote named %s (it has %s)", name, strings.Join(names, ", "))
		}
		delete(p.Remotes, name)
		if len(p.Remotes) == 0 {
			p.Remotes = nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "removed remote %s from initech.yaml; nothing on it was stopped\n", name)
	signalRemotesReload(out, name)
	return nil
}

func remoteConfigPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return config.Discover(wd)
}

// signalRemotesReload asks window 1 to re-read initech.yaml now. Window 1
// would see the change on its own within a few seconds; the signal makes it
// immediate and reports what it did.
func signalRemotesReload(out interface{ Write([]byte) (int, error) }, name string) {
	resp, err := ipcCall(tui.IPCRequest{Action: "remotes_reload"})
	switch {
	case err != nil:
		fmt.Fprintf(out, "initech is not running here; the next start reads %s\n", name)
	case !resp.OK && strings.HasPrefix(resp.Error, "unknown action"):
		fmt.Fprintf(out, "this initech reads remotes only at start: restart it to apply %s\n", name)
	case !resp.OK:
		fmt.Fprintf(out, "window 1: %s\n", resp.Error)
	default:
		fmt.Fprintf(out, "window 1: %s\n", resp.Data)
	}
}
