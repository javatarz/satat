# LiteLLM as model router

Satat routes all LLM traffic through a self-hosted LiteLLM proxy on the VM rather than calling the LLM Gateway directly. LiteLLM provides three tiered model profiles (cheap/standard/expensive) with cost-based automatic selection, a hard monthly budget cap that cannot be exceeded, and spend tracking. Without LiteLLM, budget enforcement would require manual monitoring; with it, Satat can be left unattended without risk of a surprise bill. The proxy also eliminates the need to change OpenHands configuration when models are added or removed from the gateway.
