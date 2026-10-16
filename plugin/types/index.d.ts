// The msgate plugin's contract: the interface it adds to `$` and the values
// it keeps in the session's $.state.

/** The providers a completion can be asked of today. */
export type MsgateProvider = 'anthropic'

/** The versions of this call's contract that msgate answers: 1. */
export type MsgateContract = 1

/**
 * What a caller says about a completion, besides the request itself.
 * Fields this version does not know are ignored.
 */
export type MsgateCompleteMeta = {
  /** The version of the contract the caller speaks. Anything else is denied before the model is called. */
  contract: MsgateContract
  /**
   * The call's stable name, recorded as the model call's call_id: the same id
   * is counted once. Matches [A-Za-z0-9][A-Za-z0-9:._-]{0,199}. A call whose id is
   * already in flight is denied before the model is called.
   */
  callId: string
  /**
   * Whose model answers. Left out it is `anthropic`, the only value accepted
   * today; any other is denied before the model is called, so the caller can
   * make the call itself.
   */
  provider?: MsgateProvider
  /**
   * Recorded with the model call. A key matches [a-z_]{1,32} and a value is at
   * most 200 characters; an entry that does not, and every entry past the
   * sixteenth, is left out of the record and never stops the call.
   */
  attrs?: { readonly [key: string]: string }
}

/** What a plugin backs one of its agent types with; see `backType`. */
export type MsgateBackedType = {
  command: 'exec' | 'review' | 'follow'
  promptMode: 'free' | 'review-target' | 'run-id'
  model?: string
  effort?: string
  sandbox?: 'read-only' | 'workspace-write'
}

/**
 * The arguments of `backType`. `type` matches ^[a-z0-9][a-z0-9-]{0,63}$; `exec`
 * needs the `free` prompt mode, `review` the `review-target` one and `follow` the
 * `run-id` one; `model`, `effort` and `sandbox` are the settings the engine runs
 * the type's agents with, and a `follow` type needs none.
 */
export type MsgateBackTypeArgs = MsgateBackedType & { type: string }

/** The arguments of `run`; the same as the run tool's. */
export type MsgateRunArgs = {
  prompt: string
  /** Continues that conversation; left out, a new one starts. */
  conversation_id?: string
  purpose?: string
  provider?: string
  model?: string
  effort?: string
  sandbox?: 'read-only' | 'workspace-write'
  cwd?: string
}

export type MsgateRunResult = {
  conversation_id: string
  run_id: string
  /** True when the run continued the conversation the call named. */
  continued: boolean
}

/** A run as `status --json` prints it: these keys, and others the binary adds. */
export type MsgateRun = {
  run_id: string
  conversation_id: string
  state: string
  outcome: string | null
  purpose?: string
  /** `plugin:<caller>` for a run started through this interface. */
  source: string
  output_path: string
  [key: string]: unknown
}

declare module 'claude-code' {
  interface EngineInterface {
    msgate: {
      /**
       * The completion `$.model.complete(request)` would make, and its result
       * exactly: the same request, the same arms (answered, api-error,
       * empty-reply, aborted), the request's timeoutMs included. A call that
       * used tokens is recorded for the session that runs it, with the caller's
       * plugin as the source (`mod:<plugin>`), on a best-effort basis.
       *
       * It rejects only with `msgate:pre-model:<why>`, the model never called,
       * or `msgate:after-model:<why>`, the model called; <why> matches
       * [a-z0-9-]+. A message of any other form says nothing about whether the
       * model was called.
       */
      complete: (args: { request: ModelCompleteRequest; meta: MsgateCompleteMeta }) => Promise<ModelCompleteResult>
      /**
       * Marks the caller's agent type `<caller>:<type>` as one the engine
       * answers, replacing an earlier call for the same type, and resolves that
       * full type. A plugin can back only its own types. It rejects with
       * `msgate:backType:<field>` naming the first argument it refuses (type,
       * command, promptMode, model, effort or sandbox).
       */
      backType: (args: MsgateBackTypeArgs) => Promise<{ type: string }>
      /**
       * The engine's release version, for example `1.0.0`. It rejects with
       * `msgate:version:unavailable` when the engine's binary cannot report it.
       */
      version: () => Promise<string>
      /**
       * Starts a background job like the run tool, with the source
       * `plugin:<caller>`. The engine does not deliver its result to Claude: the
       * caller reads it with `jobs`. It rejects with `msgate:run:<why>`.
       */
      run: (args: MsgateRunArgs) => Promise<MsgateRunResult>
      /**
       * `list` resolves the runs the caller admitted through `run` this session (the
       * 200 most recent), newest first, read by id; `status` one such
       * run; `result` its output (cut at 64 KiB, with a note naming the file);
       * `cancel` the confirmation of the request. `status`, `result` and
       * `cancel` reject a run the caller did not start with `msgate:jobs:not-yours`.
       * Other refusals are `msgate:jobs:<why>`.
       */
      jobs: {
        (args: { action: 'list' }): Promise<{ runs: MsgateRun[] }>
        (args: { action: 'status'; run_id: string }): Promise<MsgateRun>
        (args: { action: 'result' | 'cancel'; run_id: string }): Promise<string>
      }
    }
  }

  interface PluginState {
    'msgate': {
      // The band's lines: one for each run another caller started with
      // --agent-feedback that is still going, as the band shows it.
      hookRuns: { run_id: string; text: string }[]
      // The ids of the runs announced as spawned, newest last, at most 500.
      spawnNotified: string[]
      // The agent types the engine answers, by full type (`<plugin>:<type>`),
      // written by `backType`.
      backedTypes: { [fullType: string]: MsgateBackedType }
      // The run ids each plugin admitted through `run`, oldest first, at most 200
      // per plugin: what `jobs` lists.
      admittedRuns: { [plugin: string]: string[] }
    }
  }
}
