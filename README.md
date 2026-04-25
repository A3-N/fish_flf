# flf (Fish Log Finder)

A native-feeling, fuzzy-searching TUI shell plugin for Fish and Kitty terminal. `flf` allows you to seamlessly search and re-run your previous terminal commands directly from your scrollback logs, heavily inspired by the standard `ctrl+r` workflow.

## Requirements
- `go`
- `fish`
- `kitty` terminal

## Installation

To automatically check dependencies, configure Kitty, build the Go binary, and install the Fish shell hooks, simply run:

```bash
make install
```

Once installed, restart your Fish shell or run the provided source command, and you can trigger the search by pressing `ctrl+g`.

## Usage

**Interactive Mode:**
Press `ctrl+g` in your Fish shell to open the interactive dropdown. Type to fuzzy search your command history, use `↑/↓` to navigate, and press `Enter` to output the selected command block.

**CLI Mode:**
You can also use the `flf` binary directly to run standard text searches against your logs:
```bash
flf <search_query>
```

## Management

To clean the built binary from the repository:
```bash
make clean
```

To fully uninstall `flf` and remove the Fish bindings:
```bash
make uninstall
```
