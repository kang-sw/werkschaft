package wsagent

import "github.com/kang-sw/devenv/internal/werk"

// WerkCatalog is what the ws adapter delivers for the builtin harness
// (werk). Its version is the harness's own line-protocol version
// (werk.ProtocolVersion), which the process reports in its init line, so the
// guaranteed range is exactly the one release the recorded fixtures cover
// (testdata is the fake Responses server of the werk tests); a ws release
// never moves it.
func WerkCatalog() *HarnessProfile {
	p := &HarnessProfile{
		Compaction: CompactionProtocol{LeadCommand: true, WorkerCommand: true, Lifecycle: true, PromptPersists: true},
		Oldest:     werk.ProtocolVersion,
		Newest:     werk.ProtocolVersion,
		Entries: []CapabilityEntry{
			{Item: ItemSkillList, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "the harness has no skill mechanism; ws's text reaches a worker in its instruction file"},
			{Item: ItemSkillInjection, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "no skills: the daemon includes the worker's playbook text in its composed instructions"},
			{Item: ItemCompaction, Role: RoleWorker, Grade: GradeNative,
				Source: "ws/compact replaces model input with a committed worker summary at an idle boundary (werk.Agent)"},
			{Item: ItemRewind, Role: RoleWorker, Grade: GradeUnsupported, Source: "no ws wiring"},
			{Item: ItemToolRemoval, Role: RoleWorker, Grade: GradeNative, Values: []string{CatalogAnyValue},
				Source: "the daemon lists its file tools according to the worker's bound authority"},
			{Item: ItemSubagentRemoval, Role: RoleWorker, Grade: GradeNative,
				Source: "the harness has no native subagent tool, so a worker delegates through agent.spawn alone by construction"},
			{Item: ItemModelEffort, Role: RoleWorker, Grade: GradeNative,
				Source: "the set command changes the model and reasoning effort of the next request (werkLoop.SetModel, SetEffort); choices come from OpenRouter's model list (WerkBackend.Choices)"},
			{Item: ItemApproval, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "the daemon's MCP admission enforces the worker's authority on file and exec calls"},
			{Item: ItemSteer, Role: RoleWorker, Grade: GradeNative,
				Source: "a steered message joins the running turn at its next model request and is reported taken (werkLoop.Steer, ClearSteered)"},
			{Item: ItemInterrupt, Role: RoleWorker, Grade: GradeNative,
				Source: "the interrupt command cancels the request and the running tool calls; the turn still ends with its turn_end line (werkLoop.Interrupt)"},
			{Item: ItemFollowUp, Role: RoleWorker, Grade: GradeSubstitute,
				Source: "the ws inbox holds the message and the runner sends it when the turn ends (Runner.Send)"},
			{Item: ItemResume, Role: RoleWorker, Grade: GradeNative,
				Source: "the runner reopens the same model history and resumes the active turn from the committed log (werk.OpenStore, Agent.runDurableTurn)"},
			{Item: ItemReasoningStream, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "provider-exposed summaries are replayed and captured for audit"},
			{Item: ItemUsageQuery, Role: RoleWorker, Grade: GradeNative,
				Source: "each completed response reports its tokens, context and the provider's cost figure (werkMapper usage)"},
		},
	}
	for _, entry := range append([]CapabilityEntry(nil), p.Entries...) {
		entry.Role = RoleLead
		switch entry.Item {
		case ItemSkillList, ItemSkillInjection:
			entry.Source = "the composed lead instructions advertise ws playbook commands"
		case ItemApproval:
			entry.Grade = GradeSubstitute
			entry.Source = "daemon-owned exec asks use the common lead approval path"
		case ItemReasoningStream:
			entry.Grade = GradeSubstitute
			entry.Source = "the common reasoning projection displays completed provider-exposed summaries"
		case ItemCompaction:
			entry.Source = "ws/compact accepts the lead's carry-forward prose and commits a checkpoint at idle"
		}
		p.Entries = append(p.Entries, entry)
	}
	return p
}
