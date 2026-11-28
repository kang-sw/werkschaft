// Embedded ws MCP & provider-response bridge extension for managed Pi v1.5.2.
// Loaded via: pi --mode rpc -ne -e builtin:mcp -e <cached-ws-bridge.mjs> ...
// Zero external npm dependencies; reads credentials exclusively from env.

export default function (pi) {
  const url = (process.env.WS_MCP_SESSION_URL || "").trim();
  const token = (process.env.WS_MCP_SESSION_TOKEN || "").trim();

  if (url && token && typeof pi.registerMcpServer === "function") {
    pi.registerMcpServer("ws", {
      url,
      headers: { Authorization: `Bearer ${token}` },
      exposure: "direct",
      lifecycle: "eager",
      clientInfo: { name: "ws-pi-rpc-bridge" },
    });
  }

  if (typeof pi.on === "function") {
    pi.on("after_provider_response", (event, ctx) => {
      try {
        const status = Number(event?.status ?? event?.response?.status ?? 0);
        if (status !== 429 && status !== 529) {
          return;
        }
        const headers = event?.headers ?? event?.response?.headers;
        let retryAfter = "";
        if (headers) {
          if (typeof headers.get === "function") {
            retryAfter =
              headers.get("retry-after") ||
              headers.get("x-ratelimit-reset-requests") ||
              "";
          } else if (typeof headers === "object") {
            for (const [key, value] of Object.entries(headers)) {
              const lower = String(key).toLowerCase();
              if (
                lower === "retry-after" ||
                (!retryAfter && lower === "x-ratelimit-reset-requests")
              ) {
                retryAfter = Array.isArray(value)
                  ? String(value[0] ?? "")
                  : String(value ?? "");
              }
            }
          }
        }
        const payload = {
          ws_pi_event: "provider_response",
          status,
          retry_after: retryAfter,
          provider: String(event?.provider ?? event?.model?.provider ?? ""),
          model: String(event?.model?.id ?? event?.model ?? ""),
        };
        if (ctx?.ui && typeof ctx.ui.notify === "function") {
          ctx.ui.notify(JSON.stringify(payload), "warning");
        }
      } catch {
        // Fire-and-forget: never block or mutate provider responses.
      }
    });
  }
}
