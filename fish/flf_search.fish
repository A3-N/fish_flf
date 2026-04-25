function flf_search --description 'Search kitty log commands interactively'
    if test "$argv[1]" = "--output"
        flf --output
    else
        flf
    end
    commandline -f repaint
end
