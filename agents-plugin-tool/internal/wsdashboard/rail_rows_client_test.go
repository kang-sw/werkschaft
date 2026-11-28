package wsdashboard

import "testing"

// Real dispatch/reconnect/hidden-region code, with a small typed input stub.
// Chromium exercises the real input parser and keyed DOM merger separately.
func TestRailRowsClientRecovery(t *testing.T) {
	src := streamClientFuncs(t, "openStream", "connected", "connect", "showRegions")
	runNode(t, "rail delta dispatch and recovery", streamPageHarness(t)+`
Date.now = function () { return now; };
var mode="session",rid="rid1",worktree="",hiddenRegions={},modeHash="m1",liveMode=false,sseOn=true;
function held() { return "rail.old,head.kept"; }
function trackerLost() {} function trackerNudge() {} function syncBadge() {} function modeChanged() {} function banner() {}
function tlHold() {} function tlCount() {} function tlShown() {} var tlHeld={};
function frag(html) { return html; } function apScan() {}
var applied=[], bases=[], baselines=[];
var railAttention={observe:function(rows,rid,baseline) { baselines.push(!!baseline); }};
function swap(name,hash,input) { applied.push([name,hash,input]); }
var WsRail={
 rowInputFull:function(html) { return {value:html}; },
 rowInputHTML:function(input) { return input.value; },
 rowInputPatch:function(base,ev) { bases.push(base); return base && ev && ev.valid ? {value:base.value+ev.add} : null; }
};
`+src+`
connect(); var s=sockets[0];
s.msg("region",{name:"rail",hash:"full",html:"A"});
check("legacy full regions remain usable",applied,[["rail","full",{value:"A"}]]);
doc.hidden=true;
s.msg("rail-rows",{hash:"one",valid:true,add:"B"});
s.msg("rail-rows",{hash:"two",valid:true,add:"C"});
check("hidden patches use newest canonical baseline",[applied.length,hiddenRegions.rail.html.value,bases.map(function(b){return b.value;})],[1,"ABC",["A","AB"]]);
doc.hidden=false;showRegions();
check("only newest complete input applies",applied[1],["rail","two",{value:"ABC"}]);
s.msg("rail-rows",{hash:"invalid",valid:false});
check("untrusted delta closes stream without applying",[s.closed,applied.length],[true,2]);
advance(0);
check("reconnect retains other hashes and opts in",sockets[1].url,"ws://localhost:1234/s/rid1/events?h="+encodeURIComponent("rail.old,head.kept")+"&topics=1&railrows=1&treerows=1&entryrows=1&panelrows=1&display=1");
// The new connection cannot use the previous one's canonical row baseline.
sockets[1].msg("rail-rows",{hash:"premature",valid:true,add:"bad"});
check("delta before first full is rejected",[sockets[1].closed,applied.length],[true,2]);
advance(0);
sockets[2].msg("region",{name:"rail",hash:"fresh",html:"D"});
sockets[2].msg("rail-rows",{hash:"next",valid:true,add:"E"});
check("full region repairs baseline",applied[3],["rail","next",{value:"DE"}]);
check("cold load baselines once, short 0ms recovery reconnect preserves seen",baselines,[true,false,false,false,false]);

// Brief drop (<= 60s, e.g. 3s daemon restart) preserves seen (baseline = false).
sockets[2].fail();
advance(3000);
sockets[3].msg("region",{name:"rail",hash:"brief",html:"F"});
check("short 3s reconnect preserves seen without re-baselining",baselines[baselines.length-1],false);

// Long outage (> 60s across repeated failed retries, e.g. sleep/wake) silently re-baselines.
sockets[3].fail();
for (var k = 0; k < 21; k++) {
  advance(3000);
  if (k < 20) sockets[sockets.length - 1].fail();
}
sockets[sockets.length - 1].msg("region",{name:"rail",hash:"wake",html:"G"});
check("long >60s outage silently re-baselines on recovery",baselines[baselines.length-1],true);
sockets[sockets.length - 1].msg("rail-rows",{hash:"after-wake",valid:true,add:"H"});
check("subsequent delta after recovery does not baseline",baselines[baselines.length-1],false);
process.stdout.write(bad.join("\n"));
`)
}
