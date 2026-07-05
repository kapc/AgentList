#!/usr/bin/env bash
# Expose local AgentList (port 3000) to the internet for Twilio webhooks.
set -euo pipefail
cd "$(dirname "$0")/.."

NGROK="./bin/ngrok"
PORT="${PORT:-3000}"
ENV_FILE=".env"

if [[ ! -x "$NGROK" ]]; then
  echo "ngrok not found. Run: curl -fsSL -o /tmp/ngrok.zip https://bin.equinox.io/c/bNyj1mQVY4c/ngrok-v3-stable-darwin-arm64.zip && unzip -o /tmp/ngrok.zip -d bin && chmod +x bin/ngrok"
  exit 1
fi

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ENV_FILE"
  set +a
fi

if [[ -z "${NGROK_AUTHTOKEN:-}" ]]; then
  echo "Missing NGROK_AUTHTOKEN in .env"
  echo ""
  echo "1. Sign up (free): https://dashboard.ngrok.com/signup"
  echo "2. Copy your authtoken: https://dashboard.ngrok.com/get-started/your-authtoken"
  echo "3. Add to .env:  NGROK_AUTHTOKEN=your_token_here"
  echo "4. Run this script again"
  exit 1
fi

"$NGROK" config add-authtoken "$NGROK_AUTHTOKEN" >/dev/null 2>&1 || true

# Stop any existing ngrok tunnel for this project.
pkill -f "./bin/ngrok http ${PORT}" 2>/dev/null || true
sleep 0.5

echo "Starting ngrok tunnel to localhost:${PORT}..."
"$NGROK" http "$PORT" --log=stdout > /tmp/agentlist-ngrok.log 2>&1 &
NGROK_PID=$!
sleep 2

PUBLIC_URL=""
for _ in $(seq 1 15); do
  PUBLIC_URL=$(curl -fsS http://127.0.0.1:4040/api/tunnels 2>/dev/null \
    | python3 -c "import sys,json; t=json.load(sys.stdin).get('tunnels',[]); print(next((x['public_url'] for x in t if x.get('public_url','').startswith('https')), ''))" 2>/dev/null || true)
  if [[ -n "$PUBLIC_URL" ]]; then
    break
  fi
  sleep 1
done

if [[ -z "$PUBLIC_URL" ]]; then
  echo "Could not read ngrok public URL. Check /tmp/agentlist-ngrok.log"
  kill "$NGROK_PID" 2>/dev/null || true
  exit 1
fi

# Update BASE_URL in .env (macOS + Linux compatible).
if grep -q '^BASE_URL=' "$ENV_FILE" 2>/dev/null; then
  if sed --version >/dev/null 2>&1; then
    sed -i "s|^BASE_URL=.*|BASE_URL=${PUBLIC_URL}|" "$ENV_FILE"
  else
    sed -i '' "s|^BASE_URL=.*|BASE_URL=${PUBLIC_URL}|" "$ENV_FILE"
  fi
else
  echo "BASE_URL=${PUBLIC_URL}" >> "$ENV_FILE"
fi

echo ""
echo "ngrok is running (pid ${NGROK_PID})"
echo "  Public URL:  ${PUBLIC_URL}"
echo "  Local UI:    http://127.0.0.1:4040"
echo "  Updated .env BASE_URL=${PUBLIC_URL}"
echo ""
echo "Next steps:"
echo "  1. Restart AgentList:  ./scripts/dev.sh"
echo "  2. Open /agents/doctor → Phone agent → copy the webhook URL"
echo "  3. Twilio Console → your number → Voice webhook → POST that URL"
echo ""
echo "Press Ctrl+C to stop ngrok (or: kill ${NGROK_PID})"
wait "$NGROK_PID"
