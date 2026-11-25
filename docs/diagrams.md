```mermaid
---
title: JAR with kagent
---
flowchart TB
    devops(["DevOps"])
    enduser(["End user"])

    subgraph deploy["Deploy"]
        direction LR
        env[".env<br/>private settings"]
        make["make deploy<br/>fills the kustomize templates"]
        gen["config/manifests/generated<br/>+ jar-mcp, Agent,<br/>ModelConfig, RemoteMCPServer"]
        env --> make --> gen
    end

    docs[/"Documents<br/>DOCS dirs on a node"/]

    subgraph nskagent["namespace kagent"]
        direction LR
        ui["kagent UI<br/>research chat, shows each tool call"]:::kagent
        agent["Librarian agent pod<br/>loops: model ↔ tools<br/>ModelConfig → Ministral<br/>RemoteMCPServer → jar-mcp"]:::kagent
        ui --> agent
    end

    subgraph nsjar["namespace jar"]
        svc["Service jarchat<br/>port 80, Tailscale"]
        chat["jar-chat (unchanged)<br/>everyday chat, one fixed pass"]:::jar
        mini["ministral-3-8b<br/>KServe, llama.cpp CUDA<br/>NVIDIA RTX 5060 Ti"]:::model
        mcp["jar-mcp (new)<br/>read-only MCP tools:<br/>search, list, context"]:::jar
        idx["jar-index Job<br/>chunk, embed, store<br/>skips if populated"]:::jar
        subgraph backend["Retrieval backend"]
            qwen["qwen3-embedding-8b<br/>KServe, llama.cpp ROCm<br/>AMD RX 6600, 4096 dims"]:::model
            duck[("DuckDB<br/>Quack :9494<br/>20 GiB PVC")]
        end
    end

    devops --> env
    gen --> nskagent
    gen --> nsjar

    enduser -- research --> ui
    enduser -- everyday chat --> svc

    svc --> chat
    chat --> mini
    chat --> backend

    agent --> mini
    agent --> mcp
    mcp --> backend

    docs --> idx
    idx --> backend

    classDef jar fill:#EAF3DE,stroke:#3B6D11,color:#173404
    classDef model fill:#E6F1FB,stroke:#185FA5,color:#042C53
    classDef kagent fill:#EEEDFE,stroke:#534AB7,color:#26215C
```
