package cliapp

import (
	"fmt"
	"os"
)

// Shell completion. Emitted as a script rather than shelling out to a
// framework, so the CLI keeps its zero-dependency, single-binary shape.

// topLevelCommands drives completion and is the single place a new command
// has to be registered for the shells to know about it.
var topLevelCommands = []string{
	"scan", "add", "remove", "status",
	"sync", "peers", "pair", "unpair", "relay",
	"snapshot", "snapshots", "rollback", "branch", "checkout", "export",
	"exclude", "link", "unlink", "links", "config",
	"daemon", "service", "upnp", "version", "help",
}

var subCommands = map[string][]string{
	"pair":    {"requests", "approve", "reject"},
	"relay":   {"status", "join", "leave"},
	"daemon":  {"start", "status", "stop"},
	"service": {"install", "uninstall", "status"},
	"exclude": {"list", "add", "remove"},
	"config":  {"list", "set"},
}

func cmdCompletion(args []string) int {
	_, args = jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, completionUsage)
		return 1
	}
	switch args[0] {
	case "bash":
		fmt.Print(bashCompletion())
	case "zsh":
		fmt.Print(zshCompletion())
	case "fish":
		fmt.Print(fishCompletion())
	default:
		fmt.Fprintln(os.Stderr, completionUsage)
		return 1
	}
	return 0
}

const completionUsage = `usage: opensave completion bash|zsh|fish

  bash:  opensave completion bash > /etc/bash_completion.d/opensave
         (or: opensave completion bash >> ~/.bashrc)
  zsh:   opensave completion zsh > "${fpath[1]}/_opensave"
  fish:  opensave completion fish > ~/.config/fish/completions/opensave.fish`

func joined(list []string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

func bashCompletion() string {
	s := "# bash completion for opensave\n_opensave() {\n"
	s += "  local cur prev\n"
	s += "  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n"
	s += "  prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n\n"
	s += "  if [ \"$COMP_CWORD\" -eq 1 ]; then\n"
	s += "    COMPREPLY=( $(compgen -W \"" + joined(topLevelCommands) + "\" -- \"$cur\") )\n"
	s += "    return\n  fi\n\n"
	s += "  case \"$prev\" in\n"
	for cmd, subs := range subCommands {
		s += "    " + cmd + ") COMPREPLY=( $(compgen -W \"" + joined(subs) + "\" -- \"$cur\") ); return ;;\n"
	}
	// Commands whose next argument is a path.
	s += "    add|export) COMPREPLY=( $(compgen -f -- \"$cur\") ); return ;;\n"
	s += "  esac\n\n"
	s += "  COMPREPLY=( $(compgen -W \"--json\" -- \"$cur\") )\n"
	s += "}\ncomplete -F _opensave opensave opensave-cli\n"
	return s
}

func zshCompletion() string {
	s := "#compdef opensave opensave-cli\n"
	s += "_opensave() {\n"
	s += "  local -a cmds\n"
	s += "  cmds=(" + joined(topLevelCommands) + ")\n"
	s += "  if (( CURRENT == 2 )); then\n"
	s += "    _describe 'command' cmds\n"
	s += "    return\n  fi\n"
	s += "  case \"${words[2]}\" in\n"
	for cmd, subs := range subCommands {
		s += "    " + cmd + ") _values 'subcommand' " + joined(subs) + " ;;\n"
	}
	s += "    add|export) _files ;;\n"
	s += "    *) _values 'flag' --json ;;\n"
	s += "  esac\n}\n_opensave \"$@\"\n"
	return s
}

func fishCompletion() string {
	s := "# fish completion for opensave\n"
	descriptions := map[string]string{
		"scan": "Auto-detect game saves", "add": "Track a save folder",
		"remove": "Stop tracking a game", "status": "Show tracked games and peers",
		"sync": "Sync now", "peers": "List devices", "pair": "Pair a device",
		"unpair": "Drop a paired device", "relay": "Internet sync",
		"snapshot": "Create a snapshot", "snapshots": "List snapshots",
		"rollback": "Restore a snapshot", "branch": "Create a branch",
		"checkout": "Switch branch", "export": "Copy a save out",
		"exclude": "Folders to skip when scanning", "link": "Treat two games as one",
		"unlink": "Undo a link", "links": "Show linked ids", "config": "Read/write settings",
		"daemon": "Run or control the daemon", "service": "Install as a service",
		"upnp": "Forward a router port", "version": "Print the version", "help": "Show help",
	}
	for _, c := range topLevelCommands {
		desc := descriptions[c]
		s += fmt.Sprintf("complete -c opensave -n __fish_use_subcommand -a %s -d '%s'\n", c, desc)
	}
	for cmd, subs := range subCommands {
		for _, sub := range subs {
			s += fmt.Sprintf("complete -c opensave -n '__fish_seen_subcommand_from %s' -a %s\n", cmd, sub)
		}
	}
	s += "complete -c opensave -l json -d 'Machine-readable output'\n"
	return s
}
