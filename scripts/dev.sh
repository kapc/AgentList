#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

export ADDR="${ADDR:-:3000}"
export BASE_URL="${BASE_URL:-http://localhost${ADDR#:}}"
export DATABASE_PATH="${DATABASE_PATH:-./agentlist.db}"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

echo "Building agentlist..."
go build -o bin/agentlist ./cmd/server

echo ""
echo "Starting AgentList on ${BASE_URL}"
echo "  Login:     ${BASE_URL}/login"
echo "  Signup:    ${BASE_URL}/signup"
echo ""
echo "Required for agent chat: OPENAI_API_KEY in .env"
echo "Optional: GOOGLE_CLIENT_ID + GOOGLE_CLIENT_SECRET for Google login"
echo ""
echo "Press Ctrl+C to stop."
echo ""
exec ./bin/agentlist
