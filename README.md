# flf (Fish Log Finder)

A native-feeling, fuzzy-searching TUI shell plugin for Fish and Kitty terminal. `flf` allows you to seamlessly search and re-run your previous terminal commands directly from your scrollback logs, heavily inspired by the standard `ctrl+r` workflow.

## Requirements
- `go`
- `fish`
- `kitty` terminal

## Installation

To automatically check dependencies, configure Kitty, build the Go binary, and install the Fish shell hooks, simply run:

```bash
# make check
make install
```

Once installed, restart your Fish shell or run the provided source command, and you can trigger the search by pressing `ctrl+g`.

## Usage

**Interactive Mode:**
- **`ctrl+g`**: Open the interactive dropdown to search your **command** history. Type to fuzzy search, use `↑/↓` to navigate, and press `Enter` to output the selected command block.
- **`ctrl+o`**: Deep search mode. Searches through both your commands **and** the terminal output they generated, displaying the matched text directly below the command.

**CLI Mode:**
You can also use the `flf` binary directly to run standard text searches against your logs:
```bash
flf <search_query>
flf --output <search_query>  # Also search the output bodies
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
