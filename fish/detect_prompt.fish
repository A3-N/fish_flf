#!/usr/bin/env fish
# Detect the shell prompt delimiter character.
# Runs fish_prompt, strips ANSI/OSC codes, and returns the last visible character.

set -l raw (fish_prompt 2>/dev/null | string collect)
set -l clean (printf '%s' $raw | string replace -ra '\e\[[0-9;]*[a-zA-Z]' '' | string replace -ra '\e\][^\a]*\a' '' | string replace -ra '\e\][^\e]*\e\\\\' '' | string trim)

if test -n "$clean"
    string sub -s -1 -- $clean
else
    echo "►"
end
