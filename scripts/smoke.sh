#!/usr/bin/env bash
# 本地真实 HTTP 面冒烟测试：构建无 tag 浏览器模式二进制，以 -no-ui 无头
# 运行并对真实进程断言（非单测 mock）：
#   - TROUBLESHOOTING 的空闲态声明（四引擎/钩子全 false，空闲零占用）；
#   - /api/self 契约字段（audio_format、port）；
#   - 写接口 Origin CSRF 中间件（恶意 Origin 403 / 无 Origin 放行进参数校验）；
#   - 手动添加对端入库；SIGTERM 优雅退出（「服务已安全退出」）。
#
# 隔离性：ANY_DEVICE_DISCOVERY_OFF=1 跳过 UDP 绑定与广播，不污染局域网、
# 不与开发机常驻实例抢 DiscoveryPort；端口默认 18099 避开默认 8080。
#
# 用法：./scripts/smoke.sh [端口]     （默认 18099；依赖 go，无需 wails tag）
set -u

PORT="${1:-18099}"
cd "$(dirname "$0")/.." || exit 1

echo ">>> 构建冒烟二进制（无 tag 浏览器模式，-no-ui 下无窗口）..."
go build -o bin/any_device-smoke main.go || exit 1

echo ">>> 启动（隔离 discovery、无头、端口 ${PORT}）..."
ANY_DEVICE_DISCOVERY_OFF=1 ./bin/any_device-smoke -no-ui -no-qr -port "$PORT" > bin/smoke.log 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null; wait $PID 2>/dev/null' EXIT
sleep 2

fail=0
check() {
  local name="$1" expect="$2" actual="$3"
  if [ "$actual" = "$expect" ]; then echo "PASS $name"; else echo "FAIL $name: 期望[$expect] 实得[$actual]"; fail=1; fi
}

echo ">>> GET /api/status"
STATUS=$(curl -s "http://127.0.0.1:$PORT/api/status")
check "status HTTP 可达" "0" "$?"
echo "$STATUS" | grep -q '"sys_audio_engine":false' && echo "PASS sys_audio_engine=false（按需启停）" || { echo "FAIL sys_audio_engine"; fail=1; }
echo "$STATUS" | grep -q '"mic_audio_engine":false' && echo "PASS mic_audio_engine=false" || { echo "FAIL mic_audio_engine"; fail=1; }
echo "$STATUS" | grep -q '"mouse_capture":false' && echo "PASS mouse_capture=false" || { echo "FAIL mouse_capture"; fail=1; }

echo ">>> GET /api/self"
SELF=$(curl -s "http://127.0.0.1:$PORT/api/self")
echo "$SELF" | grep -q '"audio_format":"opus-48000-stereo"' && echo "PASS audio_format=opus-48000-stereo" || { echo "FAIL audio_format: $(echo "$SELF" | grep -o '"audio_format":"[^"]*"')"; fail=1; }
echo "$SELF" | grep -q "\"port\":$PORT" && echo "PASS port=$PORT" || { echo "FAIL port"; fail=1; }

echo ">>> GET /api/peers"
PEERS=$(curl -s "http://127.0.0.1:$PORT/api/peers")
echo "$PEERS" | grep -q '"total":0' && echo "PASS peers total=0" || { echo "FAIL peers: $PEERS"; fail=1; }

echo ">>> POST /api/peers/add（恶意 Origin → 应 403）"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Origin: http://evil.com' -H 'Content-Type: application/json' -d '{"target":"192.0.2.1:9999"}' "http://127.0.0.1:$PORT/api/peers/add")
check "恶意 Origin 被拒" "403" "$CODE"

echo ">>> POST /api/peers/add（无 Origin 的 curl → 应 200）"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{"target":"192.0.2.1:9999","name":"冒烟对端"}' "http://127.0.0.1:$PORT/api/peers/add")
check "无 Origin 放行" "200" "$CODE"

echo ">>> POST /api/peers/map（恶意 Origin → 应 403）"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Origin: http://evil.com' -H 'Content-Type: application/json' -d '{"peer_id":"manual-x","execute_mouse":true}' "http://127.0.0.1:$PORT/api/peers/map")
check "map 恶意 Origin 被拒" "403" "$CODE"

echo ">>> POST /api/peers/map（无 Origin + 空 body → 应 400 而非 403）"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{}' "http://127.0.0.1:$PORT/api/peers/map")
check "map 无 Origin 进参数校验" "400" "$CODE"

echo ">>> 验证手动对端已入库"
curl -s "http://127.0.0.1:$PORT/api/peers" | grep -q '"total":1' && echo "PASS 手动对端出现在列表" || { echo "FAIL 手动对端未入库"; fail=1; }

echo ">>> SIGTERM 优雅退出"
kill -TERM $PID
for i in 1 2 3 4 5 6 7 8 9 10; do kill -0 $PID 2>/dev/null || break; sleep 0.5; done
if grep -q "服务已安全退出" bin/smoke.log; then echo "PASS 优雅退出"; else echo "FAIL 未见「服务已安全退出」（bin/smoke.log）"; fail=1; fi
trap - EXIT
kill $PID 2>/dev/null
rm -f bin/any_device-smoke bin/smoke.log bin/any_device.log

if [ "$fail" = "0" ]; then echo "=== SMOKE-ALL-PASS ==="; else echo "=== SMOKE-FAILED ==="; exit 1; fi
