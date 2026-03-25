## Changelog
* 7a713829c1ebe7cba6ff79f218941e63cce46b00 fix: send IPC text through emulator key path instead of raw PTY write
* afffc2a1e14e8f9c6eb172b6427bce437e5f51d6 IPC: Unix socket for inter-agent messaging
* b908353adf6a77b55973351eba6d998dbd3ab9b6 add restart command (backtick + r) to kill and relaunch focused pane
* db24473c06f96a168a505a398f3c1b8067a8513d hide session descriptions from title bar (not reliable enough yet)
* 31af218469b9e8cf779db58412367c28efb66467 filter out Claude status bar from session description
* b3890b2cded2a76c66d6331dc65443ae1420789e add claude_command config for custom base command (e.g. ccs personal)
* dec2319e9f2a5b34bec9d2ad906b34e68ad7f6aa Revert "remove responseLoop that caused stray 'C' on startup"
* 86ca571a7ff76f02c5616fb3a3282faef48f6c11 Revert "launch claude directly without shell wrapper (fixes stray C)"
* 5d036d729e316f11980810162ffbf25850f07718 launch claude directly without shell wrapper (fixes stray C)
* 5e2451c8bbf26b2b3d513da5a13cd1c22f93b7cb remove responseLoop that caused stray 'C' on startup
* 0d19a69fe6922357a4d9281bf9456d9f90028ae1 configurable claude_args in initech.yaml
* 4e4f7225b800823630d608e230e9c1694b4ca639 make TUI the default command (no subcommand needed)
* 0bfbb3b8da00fbdd3ce277fc4c4ccb3942fa32cc Merge spike/tui-runtime: TUI terminal multiplexer replacing tmux
* 48467ca78aa129dbb0e6c5ca92960b516e53da64 tui: use lower one-eighth block for title bar padding effect
* a7be13c94e6f3e4578ab0eb2d9f7f7f743cce444 tui: persist session descriptions through layout changes
* 03245591b3022f72bd20f433a2469ad2e2bda1a3 tui: session descriptions in title bar, fix idle detection
* 36c913c9a5260d67cb710a1589626908ffac29a6 tui: fix Claude Code rendering and activity detection
* e0d752f12574b3532ebef0a9c96fc298af9d8336 tui: activity detection via JSONL, mouse selection, UI refinements
* 86d187b6b2afe581dfa7b26a8d957281234f36dd spike: TUI terminal multiplexer replacing tmux
* a13f5118ebdc2dad4ae52f85bb3fc778b8da2804 initech: bootstrap initech
