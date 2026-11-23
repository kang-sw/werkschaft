package wsagent

import "github.com/kang-sw/devenv/internal/werk"

// WerkCatalog is what the ws adapter delivers for the builtin harness
// (Werk). The guaranteed version is the typed event protocol tested by
// the scripted Responses fixtures.
func WerkCatalog() *HarnessProfile {
	p := &HarnessProfile{
		Compaction: CompactionProtocol{LeadCommand: true, WorkerCommand: true, Lifecycle: true, PromptPersists: true},
		Oldest:     werk.ProtocolVersion,
		Newest:     werk.ProtocolVersion,
		Entries: []CapabilityEntry{
			{Item: ItemSkillList, Role: RoleWorker, Grade: GradeUnsupported,
				Source: "the model receives the Werk skill index, which has no ws listing control"},
			{Item: ItemSkillInjection, Role: RoleWorker, Grade: GradeNative,
				Source: "the Werk loop indexes available skills and load_skill reads an indexed body on request"},
			{Item: ItemCompaction, Role: RoleWorker, Grade: GradeNative,
				Source: "ws/compact replaces model input with a committed worker summary at an idle boundary (werk.Agent)"},
			{Item: ItemRewind, Role: RoleWorker, Grade: GradeUnsupported, Source: "no ws wiring"},
			{Item: ItemToolRemoval, Role: RoleWorker, Grade: GradeNative, Values: []string{"exec.run", "exec.shell", "exec.spawn", "exec.write", "fs.edit", "fs.write"},
				Source: "the Werk loop omits exec launch and input tools at read-only authority, and MCP admission omits file mutations"},
			{Item: ItemSubagentRemoval, Role: RoleWorker, Grade: GradeNative,
				Source: "the harness has no native subagent tool, so a worker delegates through agent.spawn alone by construction"},
			{Item: ItemModelEffort, Role: RoleWorker, Grade: GradeNative,
				Source: "the set command changes the model and reasoning effort of the next request (werkLoop.SetModel, SetEffort); choices come from the selected provider (WerkBackend.Choices)"},
			{Item: ItemApproval, Role: RoleWorker, Grade: GradeSubstitute,
				Source: "Runner.ApproveExec auto-admits worker exec calls after MCP filters read-only workers; file calls enforce the bound authority"},
			{Item: ItemSteer, Role: RoleWorker, Grade: GradeNative,
				Source: "a steered message joins the running turn at its next model request and is reported taken (werkLoop.Steer, ClearSteered)"},
			{Item: ItemInterrupt, Role: RoleWorker, Grade: GradeNative,
				Source: "the interrupt command cancels the request and the running tool calls; the loop emits the interrupted turn's end event (werkLoop.Interrupt)"},
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
		case ItemSkillList:
			entry.Source = "the model receives the Werk skill index, with ws's workflow manual supplied separately"
		case ItemToolRemoval:
			entry.Values = append(append([]string(nil), entry.Values...), "exec.stop", "exec.abort")
			entry.Source = "the Werk loop and daemon omit file and exec mutations for read-only leads"
		case ItemApproval:
			entry.Grade = GradeNative
			entry.Source = "the Werk loop applies lead modes and requests binary approval before file or exec mutations through the hosted approval control"
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
