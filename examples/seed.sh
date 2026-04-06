#!/usr/bin/env bash
# Seed a memex knowledge base with example data.
#
# Usage:
#   export GEMINI_API_KEY="your-key"
#   ./examples/seed.sh
#
# This creates a "demo" KB with Gemini and stores sample memories
# covering people, projects, tools, and events.

set -euo pipefail
MEMEX=${MEMEX:-./memex}

echo "==> Creating demo KB with Gemini..."
$MEMEX kb create demo \
  --embed gemini/gemini-embedding-001 \
  --llm gemini/gemini-2.5-flash \
  --name "Demo Knowledge Base" \
  --desc "Example data for testing memex" \
  2>/dev/null || echo "    (KB 'demo' already exists, skipping)"

echo ""
echo "==> Storing example memories..."

# People and roles
$MEMEX store "Alice Chen is a senior backend engineer at Acme Corp. She specializes in distributed systems and has been leading the migration from monolith to microservices." --kb demo
echo "  [1/10] Alice Chen stored"

$MEMEX store "Bob Martinez manages the infrastructure team at Acme Corp. He introduced Kubernetes to the company in 2024 and oversees all cloud deployments on GCP." --kb demo
echo "  [2/10] Bob Martinez stored"

$MEMEX store "Carol Wu is a machine learning engineer at Acme Corp. She built the recommendation engine that increased user engagement by 35%. She reports to David Park." --kb demo
echo "  [3/10] Carol Wu stored"

# Projects
$MEMEX store "Project Atlas is Acme Corp's initiative to rebuild their data pipeline using Apache Kafka and Flink. Alice Chen is the tech lead, and Bob Martinez handles the infrastructure provisioning. The project started in January 2025 and targets Q3 completion." --kb demo
echo "  [4/10] Project Atlas stored"

$MEMEX store "Project Lighthouse is the company's AI-powered search feature. Carol Wu leads the ML components while Alice Chen handles the backend API integration. It uses embeddings from a fine-tuned BERT model and stores vectors in a custom HNSW index." --kb demo
echo "  [5/10] Project Lighthouse stored"

# Technical decisions
$MEMEX store "The team decided to use Go for all new microservices instead of Java. The main reasons were faster compile times, simpler deployment with static binaries, and better performance for I/O-bound workloads. This decision was approved by Bob Martinez and CTO Sarah Kim." --kb demo
echo "  [6/10] Go decision stored"

$MEMEX store "Acme Corp uses PostgreSQL as the primary database and Redis for caching. They evaluated CockroachDB for multi-region but decided the operational complexity wasn't worth it for their current scale of 50M monthly active users." --kb demo
echo "  [7/10] Database decisions stored"

# Events and meetings
$MEMEX store "In the Q1 2025 architecture review, the team identified three critical issues: the authentication service has a single point of failure, the search indexing pipeline has a 6-hour lag, and the notification system drops messages under high load. Alice and Bob were assigned to fix the auth SPOF." --kb demo
echo "  [8/10] Architecture review stored"

$MEMEX store "Carol Wu presented her research on retrieval-augmented generation at the internal tech talk on March 15, 2025. She demonstrated how combining dense vector search with BM25 improves answer quality by 22% compared to vector-only retrieval. The approach was approved for integration into Project Lighthouse." --kb demo
echo "  [9/10] RAG tech talk stored"

# Relationships and context
$MEMEX store "David Park is the VP of Engineering at Acme Corp. He manages Alice Chen, Bob Martinez, and Carol Wu's teams. He previously worked at Google on the Spanner team and joined Acme in 2023. He is pushing for the company to adopt an internal developer platform based on Backstage." --kb demo
echo "  [10/10] David Park stored"

echo ""
echo "==> Done! Try these commands:"
echo ""
echo "  $MEMEX search 'who works on Project Atlas' --kb demo"
echo "  $MEMEX search 'what database does Acme use' --kb demo"
echo "  $MEMEX search 'what did Carol present' --kb demo"
echo "  $MEMEX tui"
