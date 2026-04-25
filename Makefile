.PHONY: build install uninstall clean check

BINARY       := flf
GOBIN        := $(HOME)/go/bin
FISH_FUNCS   := $(HOME)/.config/fish/functions
FISH_CONFD   := $(HOME)/.config/fish/conf.d
LOG_DIR      := $(HOME)/.config/fish/logs
FLF_CONF     := $(HOME)/.config/fish
KITTY_CONF   := $(HOME)/.config/kitty/kitty.conf
KITTY_MAP    := map ctrl+shift+s launch --stdin-source=@screen_scrollback --stdin-add-formatting sh -c 'cat > "$$HOME/.config/fish/logs/flf-$$(date +\%F-\%H\%M\%S).log"'

# ── dependency checks ─────────────────────────────────────

check:
	@printf "\033[1m  Checking dependencies...\033[0m\n"
	@command -v go   >/dev/null 2>&1 && printf "  \033[32m✓\033[0m go\n"       || { printf "  \033[31m✗ go not found\033[0m\n";    exit 1; }
	@command -v fish >/dev/null 2>&1 && printf "  \033[32m✓\033[0m fish\n"     || { printf "  \033[31m✗ fish not found\033[0m\n";  exit 1; }
	@command -v kitty >/dev/null 2>&1 && printf "  \033[32m✓\033[0m kitty\n"   || { printf "  \033[31m✗ kitty not found\033[0m\n"; exit 1; }
	@test -f $(KITTY_CONF) && printf "  \033[32m✓\033[0m kitty.conf\n"         || { printf "  \033[31m✗ kitty.conf not found at $(KITTY_CONF)\033[0m\n"; exit 1; }
	@grep -q 'flf-' $(KITTY_CONF) && printf "  \033[32m✓\033[0m kitty scrollback-save mapping\n" || { \
		printf "  \033[33m⚠ kitty scrollback-save mapping not found\033[0m\n"; \
		printf "    Adding it to $(KITTY_CONF)...\n"; \
		printf '\n# flf — save scrollback to log\n$(KITTY_MAP)\n' >> $(KITTY_CONF); \
		printf "  \033[32m✓\033[0m mapping added (reload kitty to activate)\n"; \
	}
	@mkdir -p $(LOG_DIR) && printf "  \033[32m✓\033[0m log directory $(LOG_DIR)\n"
	@mkdir -p $(FLF_CONF)
	@DELIM=$$(fish fish/detect_prompt.fish 2>/dev/null) || DELIM="►"; \
		printf "%s" "$$DELIM" > $(FLF_CONF)/prompt; \
		printf "  \033[32m✓\033[0m prompt delimiter detected: \033[1m\033[36m%s\033[0m\n" "$$DELIM"
	@printf "\033[1m  All checks passed.\033[0m\n\n"

# ── build / install ───────────────────────────────────────

build: check
	@echo "Building $(BINARY)..."
	@go build -o $(BINARY) .
	@echo "Done → ./$(BINARY)"

install: build
	@mkdir -p $(GOBIN)
	@cp $(BINARY) $(GOBIN)/$(BINARY)
	@printf "  \033[32m✓\033[0m binary   → $(GOBIN)/$(BINARY)\n"
	@mkdir -p $(FISH_FUNCS)
	@cp fish/flf_search.fish $(FISH_FUNCS)/flf_search.fish
	@printf "  \033[32m✓\033[0m function → $(FISH_FUNCS)/flf_search.fish\n"
	@mkdir -p $(FISH_CONFD)
	@cp fish/flf_key_binding.fish $(FISH_CONFD)/flf_key_binding.fish
	@printf "  \033[32m✓\033[0m keybind  → $(FISH_CONFD)/flf_key_binding.fish\n"
	@printf "  \033[32m✓\033[0m config   → $(FLF_CONF)/prompt\n"
	@printf "\n\033[1m  Installed! Restart fish or run:\033[0m\n"
	@printf "    source $(FISH_CONFD)/flf_key_binding.fish\n"
	@printf "  Then press \033[1mctrl+g\033[0m to search logs.\n\n"

uninstall:
	@rm -f $(GOBIN)/$(BINARY)
	@rm -f $(FISH_FUNCS)/flf_search.fish
	@rm -f $(FISH_CONFD)/flf_key_binding.fish
	@rm -f $(FLF_CONF)/prompt
	@printf "  \033[32m✓\033[0m Uninstalled flf\n"

clean:
	@rm -f $(BINARY)
	@echo "Cleaned."
