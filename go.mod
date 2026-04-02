module github.com/nmelo/initech

go 1.25.0

require (
	github.com/charmbracelet/ultraviolet v0.0.0-20260303162955-0b88c25f3fff
	github.com/charmbracelet/x/ansi v0.11.6
	github.com/charmbracelet/x/vt v0.0.0-20260323091123-df7b1bcffcca
	github.com/creack/pty v1.1.24
	github.com/creativeprojects/go-selfupdate v1.5.2
	github.com/gdamore/tcell/v2 v2.13.8
	github.com/hashicorp/yamux v0.1.2
	github.com/spf13/cobra v1.10.2
	golang.org/x/term v0.38.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	code.gitea.io/sdk/gitea v0.22.1 // indirect
	github.com/42wim/httpsig v1.2.3 // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.2 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.9.0 // indirect
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/go-fed/httpsig v1.1.0 // indirect
	github.com/google/go-github/v74 v74.0.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/hashicorp/go-version v1.8.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	gitlab.com/gitlab-org/api/client-go v1.9.1 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/oauth2 v0.34.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.32.0 // indirect
	golang.org/x/time v0.14.0 // indirect
)

replace github.com/charmbracelet/x/vt => ../.bench/scrollback-exp/forks/x-sparse/vt
