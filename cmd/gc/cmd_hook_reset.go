package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/hooks"
	"github.com/spf13/cobra"
)

func newHookResetCmd(stdout, stderr io.Writer) *cobra.Command {
	var hookProviders []string
	cmd := &cobra.Command{
		Use:   "reset [agent]",
		Short: "Remove and reinstall Gas City provider hook files for an agent",
		Long: `Removes Gas City–managed hook artifacts for the agent's install_agent_hooks
list, then reinstalls them from the embedded pack (same as the controller).

Overlay hooks (codex, gemini, cursor, copilot, opencode, pi, omp) are removed
from the agent's configured work directory. Claude: only city .gc/settings.json
is removed; .claude/settings.json and hooks/claude.json are preserved so
install can merge them again.

Use --hook-providers when install_agent_hooks is not set (or to reinstall only
a subset), e.g. gc hook reset my-agent --hook-providers=pi

If your agent is literally named "reset", set GC_AGENT or GC_ALIAS and run
"gc hook reset" with no positional argument so the name is not parsed as this
subcommand.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdHookReset(args, hookProviders, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&hookProviders, "hook-providers", nil,
		"comma-separated hook provider names to reinstall (e.g. pi,codex); overrides install_agent_hooks for this run")
	return cmd
}

func cmdHookReset(args []string, hookProvidersFlag []string, stdout, stderr io.Writer) int {
	agentName, _ := hookCLIAgentFromArgs(args)
	if strings.TrimSpace(agentName) == "" {
		fmt.Fprintln(stderr, "gc hook reset: agent not specified (set GC_AGENT or GC_ALIAS, or pass as argument)") //nolint:errcheck // best-effort stderr
		return 1
	}

	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc hook reset: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	cfg, err := loadCityConfig(cityPath, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gc hook reset: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	resolveRigPaths(cityPath, cfg.Rigs)

	if citySuspended(cfg) {
		fmt.Fprintln(stderr, "gc hook reset: city is suspended") //nolint:errcheck // best-effort stderr
		return 1
	}

	a, ok := resolveAgentIdentity(cfg, agentName, currentRigContext(cfg))
	if !ok {
		// Common mistake: gc hook reset pi — "pi" is a provider, not an agent name.
		if len(args) == 1 && hooks.Validate([]string{strings.TrimSpace(args[0])}) == nil {
			envAgent := strings.TrimSpace(os.Getenv("GC_ALIAS"))
			if envAgent == "" {
				envAgent = strings.TrimSpace(os.Getenv("GC_AGENT"))
			}
			fmt.Fprintf(stderr, "gc hook reset: %q is a hook provider name, not an agent in city.toml.\n", args[0]) //nolint:errcheck // best-effort stderr
			if envAgent != "" {
				fmt.Fprintf(stderr, "Try: gc hook reset %s --hook-providers=%s\n", envAgent, strings.TrimSpace(args[0])) //nolint:errcheck // best-effort stderr
			} else {
				fmt.Fprintf(stderr, "Try: gc hook reset <agent-name> --hook-providers=%s\n", strings.TrimSpace(args[0])) //nolint:errcheck // best-effort stderr
			}
			return 1
		}
		fmt.Fprintf(stderr, "gc hook reset: agent %q not found in config\n", agentName) //nolint:errcheck // best-effort stderr
		return 1
	}

	if isAgentEffectivelySuspended(cfg, &a) {
		fmt.Fprintf(stderr, "gc hook reset: agent %q is suspended\n", agentName) //nolint:errcheck // best-effort stderr
		return 1
	}

	resolver := func(name string) string { return config.BuiltinFamily(name, cfg.Providers) }

	ih := normalizeHookProviderFlag(hookProvidersFlag)
	if len(ih) == 0 {
		ih = config.ResolveInstallHooks(&a, &cfg.Workspace)
	}
	if len(ih) == 0 {
		fmt.Fprintf(stderr, "gc hook reset: agent %q has no install_agent_hooks in city.toml.\n", agentName) //nolint:errcheck // best-effort stderr
		fmt.Fprintf(stderr, "Add install_agent_hooks (workspace or per-agent), or run:\n  gc hook reset %s --hook-providers=pi\n", agentName) //nolint:errcheck // best-effort stderr
		return 1
	}
	if err := hooks.ValidateWithResolver(ih, resolver); err != nil {
		fmt.Fprintf(stderr, "gc hook reset: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	cityName := loadedCityName(cfg, cityPath)
	workDir, err := resolveConfiguredWorkDir(cityPath, cityName, a.QualifiedName(), &a, cfg.Rigs)
	if err != nil {
		fmt.Fprintf(stderr, "gc hook reset: workdir: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	if err := hooks.ResetAndInstallWithResolver(fsys.OSFS{}, cityPath, workDir, ih, resolver); err != nil {
		fmt.Fprintf(stderr, "gc hook reset: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	fmt.Fprintf(stdout, "gc hook reset: reinstalled hooks for agent %q: %s\n", a.QualifiedName(), strings.Join(ih, ", ")) //nolint:errcheck // best-effort stdout
	return 0
}

// normalizeHookProviderFlag flattens cobra StringSlice values (each may be
// comma-separated) into trimmed non-empty provider names.
func normalizeHookProviderFlag(flag []string) []string {
	if len(flag) == 0 {
		return nil
	}
	var out []string
	for _, raw := range flag {
		for _, part := range strings.Split(raw, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
