# balefire 烽火 

## 任务传递通道设计文档

在不稳定网络环境（如高延迟、频繁掉线、带宽波动）下运行的任务传递通道，核心目标是确保任务的可靠性（Reliability）、幂等性（Idempotency）和状态一致性（Consistency）

## 高可用任务传递通道的设计方案

1. 核心架构设计
   在不稳定网络中，不能依赖“发送即成功”的假设。我们需要引入本地持久化和双向确认机制。

A. 客户端（发送方）：本地发件箱模式 (Outbox Pattern)

- 本地数据库暂存：任务产生后，先存入本地数据库（NutsDB/Pebble），状态标记为 PENDING。
  - 抽象数据库接口，并实现两套数据库调用实现
- 断线重传引擎：监听网络状态。一旦检测到网络恢复，轮询本地未完成任务并尝试发送。
- 分片上传：对于大体积任务（如文件或大数据包），进行逻辑分片（Chunking），支持断点续传。

B. 传输层：协议选择与优化

- 压缩技术：使用 Zstd 或 Protobuf 进行高效压缩，减少在窄带宽下的传输时长，降低被中断的概率。

2. 可靠性保障机制

   重试策略：指数退避与随机抖动简单的固定间隔重试会导致“惊群效应”。应采用**指数退避（Exponential Backoff）**算法：$$t = min(cap, base \cdot 2^{n}) + jitter$$其中 $n$ 是重试次数，$jitter$ 是随机波动值，用以分散服务器压力。

幂等性设计
在不稳定网络中，ACK（确认包）极易丢失，导致发送方重复发送。

- 全局唯一任务 ID (UUID)：服务端通过任务 ID 建立去重表。
- 状态机控制：服务端接收到任务后，若发现该 ID 已存在且状态为“已完成”，则直接返回成功 ACK，不再重复执行逻辑。

3. 关键功能模块
   功能模块,描述
   心跳检测 (Keep-alive),动态调整心跳频率。网络差时延长心跳，避免无效连接重连。
   优先级队列,确保高优先级的控制指令优先于大流量数据任务传输。
   死信队列 (DLQ),超过最大重试次数（如 10 次）的任务转入死信队列，人工干预或延后处理。
   预写日志 (WAL),记录任务执行的每一个物理步骤，确保崩溃恢复后能从断点继续。

4. 极端情况应对方案

- 长期离线：本地存储需设置 TTL (生存时间) 或容量上限，防止撑爆客户端磁盘。
- 弱网环境：自动切换到“极简模式”，仅传输任务元数据，延迟传输非核心附件。
- ACK 丢失优化：采用 累积确认（Cumulative ACK），发送方发送 1-5 号分片，接收方只需回复“已收到 5 号”，表示前 5 个都已收到，减少交互次数。

补充：

1. 核心架构：混合网格拓扑 (Hybrid Mesh)

在 P2P 环境下，不再由单一服务器负责转发，而是构建一个逻辑覆盖网络 (Overlay Network)。

A. 角色定义

- 源节点 (Source)：任务发起者。
- 中继节点 (Relay/POP)：协助转发数据的节点。
- 目标节点 (Target)：任务接收者。
- 协调器 (Tracker/Bootstrapper)：可选，帮助节点发现彼此并维护全局路由表。

B. 节点发现机制

- DHT (Distributed Hash Table)：如 Kademlia 算法。节点通过维护局部路由表，可以找到逻辑距离目标节点更近的“邻居”。
- mDNS / 局域网发现：在局域网内部，通过广播快速建立 P2P 链路，避开拥堵的公网出口。

2. 关键技术：NAT 穿越与中继切换

由于节点通常位于防火墙或 NAT 后，直接连接可能失败。

1. 打洞 (NAT Traversal)：使用 go-libp2p 提供的 NAT 能力进行打洞实现连通。
2. 动态 POP 选择：如果 A 无法直接连接 B，则搜索同时与 A 和 B 保持连接的节点 C。此时 C 自动升级为 逻辑 POP。

- 选择准则：$Score = \alpha \cdot Bandwidth + \beta \cdot Latency - \gamma \cdot PacketLoss$
- 优先选择带宽充足、丢包率低的节点作为中继。

3. 任务分发策略

为了应对不稳定网络，建议采用以下两种分发模式：

A. 洪泛/流言协议 (Gossip Protocol)

适用于小体积任务或状态更新。

- 源节点将任务传给 $k$ 个邻居。
- 邻居收到后，排除来源节点，再随机传给其他 $k$ 个邻居。
- 优点：极高的冗余度，局部节点掉线不影响全局传递。

B. 分片多路径传输 (Multi-path Swarming)

适用于大体积任务。

- 将任务切分为 $N$ 个分片。
- 通过不同的 POP 节点并行传输不同分片。
- 纠错机制：引入 前向纠错码 (FEC) 或 喷泉码 (Fountain Codes)。即使只收到 80% 的分片，也能通过数学计算还原出 100% 的数据，无需请求重传。

4. 安全设计

- 端到端加密 (E2EE)：中继节点（POP）仅负责传递，无法解密任务内容。
- 签名校验：每个任务包必须包含源节点的私钥签名，防止中继节点篡改数据。
- 防放大攻击：限制每个节点每秒可转发的请求数，防止黑客利用 P2P 网络发起 DDoS。

通过上述设计，balefire 烽火任务传递通道能够在不稳定网络环境下，确保任务的可靠传递和执行，提升整体系统的健壮性和用户体验。

底层传输(Transport)实现请使用 Libp2p (https://github.com/libp2p/go-libp2p) 作为底层网络通信库，以简化 P2P 网络的实现和管理。

## 技术实现细节 (Technical Implementation Details)

### 1. 通信协议 (Protocol Buffers)

为了保证高效传输和协议的可扩展性，我们使用 Protobuf 定义消息格式。

```protobuf
syntax = "proto3";
package transport.v1;

enum MessageType {
  PING = 0;       // 心跳检测
  PONG = 1;       // 心跳响应
  DATA = 2;       // 数据载荷
  ACK = 3;        // 确认回执
}

message Message {
  string id = 1;                  // UUID，全局唯一消息ID
  MessageType type = 2;           // 消息类型
  bytes payload = 3;              // 实际数据载荷
  int64 timestamp = 4;            // 发送时间戳 (Unix Nano)
  map<string, string> metadata = 5; // 元数据 (TraceID, Priority, etc.)
  bytes signature = 6;            // Ed25519 签名，防篡改
}
```

### 2. 本地存储选型

为了实现可靠的任务持久化和发件箱模式 (Outbox Pattern)，我们选用嵌入式键值数据库 **NutsDB**。

- **优势**: 纯 Go 实现，无需 CGO，支持事务，性能足够满足客户端需求。
- **存储结构**:
  - `Bucket: PendingTasks`: 存储待发送任务 (Key: TaskID, Value: SerializedTask)
  - `Bucket: SentTasks`: 存储已发送但未确认的任务 (Key: TaskID, Value: SerializedTask + SendTime)
  - `Bucket: Dedup`: 去重表 (Key: TaskID, Value: ReceivedTime, TTL: 24h)

### 3. Libp2p 模块使用

- **Host**: 基本节点，管理连接和流。
- **Stream**: 使用流式传输 Protobuf 编码的消息。
- **DHT (Kademlia)**: 用于节点发现和路由。
- **NAT**: 启用 AutoNAT 和 Relay Service (Circuit Relay v2) 穿透防火墙。