package codex

// RelayPrompt asks a parent session to hand prompt to its sub-agent addressed
// as address, wait for that agent's final answer and reply with it as it was
// given. Codex refuses to resume a sub-agent's own thread, but the parent can
// still task it by name. It must be followup_task: send_message passes text to
// a running agent without triggering a turn, which can leave an idle child
// untriggered and the parent waiting for an answer that never comes.
func (adapter) RelayPrompt(address string, prompt []byte) []byte {
	head := "Give the task below to your existing agent \"" + address + "\" with `followup_task`; " +
		"do not use `send_message` and do not spawn a new agent. " +
		"Then call `wait_agent` until that agent has given its final answer to this task, waiting again if it has not finished. " +
		"Reply with that agent's final answer exactly as it gave it, and nothing else.\n" +
		"\n" +
		"Task for " + address + ":\n"
	return append([]byte(head), prompt...)
}
