# Working on msgate

## Versions

`VERSION` holds a [Semantic Versioning 2.0.0](https://semver.org) version, and
the release gate refuses anything else.

Before cutting a release or changing the version, fetch and follow https://raw.githubusercontent.com/JustAzul/agent-docs/main/plugin-releasing-dist.md (`curl -fsSL`; it always serves the latest version). If the fetch fails, stop and tell the owner; never release without it.

A push to `main` publishes nothing; only a release commit does, and the guide defines it.

## Before pushing a change to the plugin

A change under `plugin/` can leave the mod unable to load, and then every hook
of the plugin is gone for the whole session and for every plugin that calls the
`msgate` interface. The plugin tests cannot see it: a test's `$` carries only
Claude Code's own interfaces, so the plugin's interface is never mounted there.

Run `make verify-plugin` before every push that touches `plugin/`. It starts
`claude -p` with this plugin, in a temporary directory with no session kept and
a main model that does not exist, so nothing is spent on a model; it reads the
debug log and fails unless the `msgate` interface is mounted with its seven
methods (`complete`, `backType`, `version`, `run`, `jobs`, `flaggedRuns`, `models`) and
the module loaded. The interface is an object of methods and nothing else: a
member that is not a function unloads the plugin.
