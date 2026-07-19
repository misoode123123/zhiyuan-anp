"""智源 ANP AI/Agent 运行时。

承载 Agent 编排（LangGraph，后续）与模型调用（LiteLLM），作为独立微服务，
由 Go 后端经 HTTP（M0）/ gRPC（后续）调用。
"""

__version__ = "0.1.0"
