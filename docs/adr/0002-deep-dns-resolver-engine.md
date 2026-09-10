# Encapsulate DNS Resolver with RFC 1035 TTL Caching and Multi-Upstream Failover

We encapsulate the DNS resolution subsystem into a deep `pkg/dns.Server` module. Previously, `StartDoHServer` leaked raw concurrency channels to callers, hardcoded a single upstream (Cloudflare) leading to 3-second query timeouts during latency spikes, and performed zero in-memory caching. The new resolver implements bounded in-memory caching with RFC 1035 TTL extraction and TID rewriting, sequential fallback to secondary DoH upstreams (Google/Quad9), and an isolated lifecycle with event streaming.
