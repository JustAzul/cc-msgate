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
    }
  }

  interface PluginState {
    'msgate': {
      // The band's lines: one for each run another caller started with
      // --agent-feedback that is still going, as the band shows it.
      hookRuns: { run_id: string; text: string }[]
      // The ids of the runs announced as spawned, newest last, at most 500.
      spawnNotified: string[]
    }
  }
}
