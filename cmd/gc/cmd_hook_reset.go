package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/hooks"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/spf13/cobra"
)

func newHookResetCmd(stdout, stderr io.Writer) *cobra.Command {
	var hookProviders []string
	var allAgents bool
	cmd := &cobra.Command{
		Use:   "reset [agent]",
		Short: "Remove and reinstall Gas City provider hook files for an agent",
		Long: `Removes Gas City–managed hook artifacts for the agent's install_agent_hooks
list, then reinstalls them from the embedded pack (same as the controller).

Overlay hooks (codex, gemini, cursor, copilot, opencode, pi, omp) are removed
from the agent's configured work directory. Claude: only city .gc/settings.json
is removed; .claude/settings.json and hooks/claude.json are preserved so
install can merge them again.

When pi is among the hook providers, after reinstall the command also merges
$HOME/.pi into the agent work directory's .pi/ tree (JSON deep-merge; other
files copied). Gas City–managed .pi/extensions/gc-hooks.js stays the embedded
version; ~/.pi/agent/sessions is not copied.

With --all-agents, every non-implicit agent in city.toml (city-scoped and
rig-scoped under rigs/) is processed: for each agent whose install_agent_hooks
includes pi (or when --hook-providers includes pi), the command resolves work
directories—including bounded pool instance dirs—and reinstalls hooks in each
distinct work_dir (same rules as a single-agent reset).

Use --hook-providers when install_agent_hooks is not set (or to reinstall only
a subset), e.g. gc hook reset my-agent --hook-providers=pi

If your agent is literally named "reset", set GC_AGENT or GC_ALIAS and run
"gc hook reset" with no positional argument so the name is not parsed as this
subcommand.`,
		Args: func(cmd *cobra.Command, args []string) error {
			all, err := cmd.Flags().GetBool("all-agents")
			if err != nil {
				return err
			}
			if all {
				if len(args) > 0 {
					return fmt.Errorf("gc hook reset: do not pass an agent name with --all-agents")
				}
				return nil
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if allAgents {
				if cmdHookResetAllAgents(hookProviders, stdout, stderr) != 0 {
					return errExit
				}
				return nil
			}
			if cmdHookReset(args, hookProviders, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&hookProviders, "hook-providers", nil,
		"comma-separated hook provider names to reinstall (e.g. pi,codex); overrides install_agent_hooks for this run")
	cmd.Flags().BoolVar(&allAgents, "all-agents", false,
		"run hook reset for every non-implicit agent (city and rig-scoped); selects agents whose install_agent_hooks includes pi unless --hook-providers overrides the list for all; resolves each work_dir including bounded pool instance dirs")
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
		fmt.Fprintf(stderr, "gc hook reset: agent %q has no install_agent_hooks in city.toml.\n", agentName)                                  //nolint:errcheck // best-effort stderr
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

// cmdHookResetAllAgents runs hook reset for every non-implicit agent whose
// resolved install_agent_hooks includes the pi family (or when --hook-providers
// includes pi), for each distinct resolved work directory.
func cmdHookResetAllAgents(hookProvidersFlag []string, stdout, stderr io.Writer) int {
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

	resolver := hooks.FamilyResolver(func(name string) string {
		return config.BuiltinFamily(name, cfg.Providers)
	})

	flagIH := normalizeHookProviderFlag(hookProvidersFlag)
	if len(flagIH) > 0 {
		if err := hooks.ValidateWithResolver(flagIH, resolver); err != nil {
			fmt.Fprintf(stderr, "gc hook reset: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
	}

	sp := newSessionProvider()
	var hadErr bool
	var ran int
	for i := range cfg.Agents {
		a := &cfg.Agents[i]
		if a.Implicit {
			continue
		}
		if isAgentEffectivelySuspended(cfg, a) {
			fmt.Fprintf(stderr, "gc hook reset: skip agent %q (suspended)\n", a.QualifiedName()) //nolint:errcheck // best-effort stderr
			continue
		}

		ih := flagIH
		if len(ih) == 0 {
			ih = config.ResolveInstallHooks(a, &cfg.Workspace)
		}
		if len(ih) == 0 {
			continue
		}
		if !installHooksIncludesPiFamily(ih, resolver) {
			continue
		}
		if len(flagIH) == 0 {
			if err := hooks.ValidateWithResolver(ih, resolver); err != nil {
				fmt.Fprintf(stderr, "gc hook reset: agent %q: %v\n", a.QualifiedName(), err) //nolint:errcheck // best-effort stderr
				hadErr = true
				continue
			}
		}

		dirs, werr := workDirsForHookResetAgent(cityPath, cfg, a, sp)
		if werr != nil {
			fmt.Fprintf(stderr, "gc hook reset: agent %q: %v\n", a.QualifiedName(), werr) //nolint:errcheck // best-effort stderr
			hadErr = true
			continue
		}
		for _, workDir := range dirs {
			if err := hooks.ResetAndInstallWithResolver(fsys.OSFS{}, cityPath, workDir, ih, resolver); err != nil {
				fmt.Fprintf(stderr, "gc hook reset: agent %q work_dir %s: %v\n", a.QualifiedName(), workDir, err) //nolint:errcheck // best-effort stderr
				hadErr = true
				continue
			}
			ran++
			fmt.Fprintf(stdout, "gc hook reset: reinstalled hooks for agent %q (%s): %s\n", a.QualifiedName(), workDir, strings.Join(ih, ", ")) //nolint:errcheck // best-effort stdout
		}
	}
	if ran == 0 && !hadErr {
		fmt.Fprintln(stdout, "gc hook reset: --all-agents: no work directories processed (no agents have pi in install_agent_hooks, or all were skipped; use --hook-providers=pi to apply pi hooks to every agent)") //nolint:errcheck // best-effort stdout
	}
	if hadErr {
		return 1
	}
	return 0
}

func installHooksIncludesPiFamily(ih []string, resolve hooks.FamilyResolver) bool {
	for _, p := range ih {
		fam := p
		if resolve != nil {
			if r := resolve(p); r != "" {
				fam = r
			}
		}
		if fam == "pi" {
			return true
		}
	}
	return false
}

func hookResetQualifiedNamesForAgent(cfg *config.City, cityPath string, a *config.Agent, sp runtime.Provider) []string {
	if !a.SupportsInstanceExpansion() {
		return []string{a.QualifiedName()}
	}
	cityName := loadedCityName(cfg, cityPath)
	sp0 := scaleParamsFor(a)
	instances := discoverPoolInstances(a.Name, a.Dir, sp0, a, cityName, cfg.Workspace.SessionTemplate, sp)
	if len(instances) == 0 {
		return []string{a.QualifiedName()}
	}
	return instances
}

func workDirsForHookResetAgent(cityPath string, cfg *config.City, a *config.Agent, sp runtime.Provider) ([]string, error) {
	cityName := loadedCityName(cfg, cityPath)
	qns := hookResetQualifiedNamesForAgent(cfg, cityPath, a, sp)
	seen := make(map[string]bool)
	var dirs []string
	for _, qn := range qns {
		wd, err := resolveConfiguredWorkDir(cityPath, cityName, qn, a, cfg.Rigs)
		if err != nil {
			return nil, fmt.Errorf("qualified %q: %w", qn, err)
		}
		if !seen[wd] {
			seen[wd] = true
			dirs = append(dirs, wd)
		}
	}
	return dirs, nil
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
