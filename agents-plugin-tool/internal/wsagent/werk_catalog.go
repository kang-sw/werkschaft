package wsagent

import "github.com/kang-sw/devenv/internal/werk"

// WerkCatalog is what the ws adapter delivers for the builtin harness
// (werk). Its version is the harness's own line-protocol version
// (werk.ProtocolVersion), which the process reports in its init line, so the
// guaranteed range is exactly the one release the recorded fixtures cover
// (testdata is the fake Responses server of the werk tests); a ws release
// never moves it.
//
// Phase 1 of 261122-feat-ws-builtin-agent-harness ships the worker role. Every
// item is stated for both roles: the lead role is Phase 2, so each lead entry
// says what the worker's wiring would be and that no lead is registered yet.
func WerkCatalog() *HarnessProfile {
	p := &HarnessProfile{
		// Compaction is Phase 2's ws/compact lifecycle: no command and no
		// lifecycle signal exist yet, so the daemon plans none for a werk
		// worker (LifecyclePolicy.NoWorkerCompactionSteer).
		Compaction: CompactionProtocol{},
		Oldest:     werk.ProtocolVersion,
		Newest:     werk.ProtocolVersion,
		Entries: []CapabilityEntry{
			{Item: ItemSkillList, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "the harness has no skill mechanism; ws's text reaches a worker in its instruction file"},
			{Item: ItemSkillInjection, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "no skills: a worker's playbook text is part of its composed instructions (werk-run --instructions)"},
			{Item: ItemCompaction, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "the common ws/compact lifecycle is Phase 2; Phase 1 has no compaction command or completion signal"},
			{Item: ItemRewind, Role: RoleWorker, Grade: GradeUnsupported, Source: "no ws wiring"},
			{Item: ItemToolRemoval, Role: RoleWorker, Grade: GradeNative, Values: []string{CatalogAnyValue},
				Source: "the harness offers the file tools its launch lists (werk-run --tools); a read-only worker is offered the read tools alone (werkToolList)"},
			{Item: ItemSubagentRemoval, Role: RoleWorker, Grade: GradeNative,
				Source: "the harness has no native subagent tool, so a worker delegates through agent.spawn alone by construction"},
			{Item: ItemModelEffort, Role: RoleWorker, Grade: GradeNative,
				Source: "the set command changes the model and reasoning effort of the next request (werkProcess.SetModel, SetEffort); choices come from OpenRouter's model list (WerkBackend.Choices)"},
			{Item: ItemApproval, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "no approval prompt exists: a worker's authority is enforced by its own file tools and by ws-mcp's refusal of mutating ws tools (werk.Files)"},
			{Item: ItemSteer, Role: RoleWorker, Grade: GradeNative,
				Source: "a steered message joins the running turn at its next model request and is reported taken (werkProcess.Steer, ClearSteered)"},
			{Item: ItemInterrupt, Role: RoleWorker, Grade: GradeNative,
				Source: "the interrupt command cancels the request and the running tool calls; the turn still ends with its turn_end line (werkProcess.Interrupt)"},
			{Item: ItemFollowUp, Role: RoleWorker, Grade: GradeSubstitute,
				Source: "the ws inbox holds the message and the runner sends it when the turn ends (Runner.Send)"},
			{Item: ItemResume, Role: RoleWorker, Grade: GradeNative,
				Source: "a process started with the recorded session id replays the conversation file's items and answers a call with no result as interrupted (werk.OpenStore, Agent.repairPending)"},
			{Item: ItemReasoningStream, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "provider-exposed summaries are replayed and captured for audit but not shown; the live reasoning summary is Phase 2's lead surface"},
			{Item: ItemUsageQuery, Role: RoleWorker, Grade: GradeNative,
				Source: "each completed response reports its tokens, context and the provider's cost figure (werkMapper usage)"},
		},
	}
	for _, entry := range append([]CapabilityEntry(nil), p.Entries...) {
		entry.Role = RoleLead
		entry.Grade = GradeUnsupported
		entry.Values = nil
		entry.Source = "no werk lead is registered: the lead role is Phase 2 of 261122-feat-ws-builtin-agent-harness"
		p.Entries = append(p.Entries, entry)
	}
	return p
}
