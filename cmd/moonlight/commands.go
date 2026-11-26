package main

import "moonlight/internal/gamma"

// commands routes a subcommand name to its handler. It is the single place a command is
// registered, so a test can prove that everything the usage text advertises is wired.
var commands = map[string]func(configPath string, args []string) error{
	"init":    cmdInit,
	"select":  cmdSelect,
	"setup":   cmdSelect,
	"apply":   cmdApply,
	"set":     cmdSet,
	"pause":   cmdPause,
	"daemon":  cmdDaemon,
	"probe":   func(c string, _ []string) error { return cmdProbe(c) },
	"preview": func(c string, _ []string) error { return cmdPreview(c) },
	"status":  func(c string, _ []string) error { return cmdStatus(c) },
	"reset":   func(c string, _ []string) error { return cmdReset(c) },
	"restore": func(c string, _ []string) error { return cmdRestore(c) },
	"resume":  func(c string, _ []string) error { return cmdResume(c) },

	"install":   func(_ string, a []string) error { return cmdInstall(a) },
	"uninstall": func(_ string, a []string) error { return cmdUninstall(a) },

	// Internal: the macOS gamma holder, started on demand by the daemon and CLI.
	"gamma-holder": func(string, []string) error { return gamma.RunHolder() },
}
