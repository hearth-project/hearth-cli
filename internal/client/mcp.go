package client

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/mcpcfg"
)

// MCPConfigPath is the agent-neutral MCP config in the library.
func MCPConfigPath(home string) string { return filepath.Join(LibraryDir(home), "mcp.toml") }

func mcpStatePath(home string) string { return filepath.Join(home, "mcp-state.json") }

// LoadMCPConfig reads config.toml [mcp], else the legacy library/mcp.toml.
// present is false when neither defines servers.
func LoadMCPConfig(home string) (cfg *mcpcfg.Config, present bool, err error) {
	if c, err := UserConfig(home); err == nil && c.MCP != nil {
		cfg = &mcpcfg.Config{Servers: map[string]mcpcfg.Server{}}
		for n, s := range c.MCP.Servers {
			cfg.Servers[n] = mcpcfg.Server{Command: s.Command, Args: s.Args, Env: s.Env, Agents: s.Agents}
		}
		return cfg, true, nil
	}
	if _, err := os.Stat(MCPConfigPath(home)); errors.Is(err, os.ErrNotExist) {
		return &mcpcfg.Config{Servers: map[string]mcpcfg.Server{}}, false, nil
	}
	cfg, err = mcpcfg.Load(MCPConfigPath(home))
	return cfg, true, err
}

// SaveMCPConfig writes the servers into config.toml [mcp].
func SaveMCPConfig(home string, cfg *mcpcfg.Config) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	c.MCP = &library.MCPConfig{Servers: map[string]library.MCPServer{}}
	for n, s := range cfg.Servers {
		c.MCP.Servers[n] = library.MCPServer{Command: s.Command, Args: s.Args, Env: s.Env, Agents: s.Agents}
	}
	if err := SaveUserConfig(home, c); err != nil {
		return err
	}
	os.Remove(MCPConfigPath(home))
	return nil
}

// RenderMCP writes config.toml [mcp] into every agent, resolving secrets.
func (e *Engine) RenderMCP(adopt bool) ([]mcpcfg.Action, error) {
	cfg, present, err := LoadMCPConfig(e.Home)
	if err != nil || !present {
		return nil, err
	}
	targets := e.MCPTargets
	if targets == nil {
		targets = EnabledMCPTargets(e.Home)
	}
	h := &recipeHost{e: e}
	st := mcpcfg.LoadState(mcpStatePath(e.Home))
	acts, err := mcpcfg.Render(cfg, targets, h.Secret, st, adopt)
	if serr := st.Save(mcpStatePath(e.Home)); err == nil {
		err = serr
	}
	return acts, err
}

// ImportMCP merges an agent's existing mcpServers file into the library,
// moving credential-looking env values into encrypted secrets.
func (e *Engine) ImportMCP(path string, overwrite bool) ([]string, []string, error) {
	servers, secrets, err := mcpcfg.ImportJSON(path)
	if err != nil {
		return nil, nil, err
	}
	cfg, _, err := LoadMCPConfig(e.Home)
	if err != nil {
		return nil, nil, err
	}
	var added, skipped []string
	for n, s := range servers {
		if _, ok := cfg.Servers[n]; ok && !overwrite {
			skipped = append(skipped, n)
			continue
		}
		cfg.Servers[n] = s
		added = append(added, n)
	}
	for name, v := range secrets {
		if err := e.SetSecret(Secret{Name: name, Value: []byte(v)}); err != nil {
			return added, skipped, err
		}
	}
	return added, skipped, SaveMCPConfig(e.Home, cfg)
}
