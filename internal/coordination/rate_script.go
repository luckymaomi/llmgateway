package coordination

import "github.com/redis/go-redis/v9"

var acquireRateScript = redis.NewScript(`
local clock = redis.call('TIME')
local now_ms = (tonumber(clock[1]) * 1000) + math.floor(tonumber(clock[2]) / 1000)
local states = {}
local retry_after_ms = 0

for i = 1, #KEYS do
  local offset = ((i - 1) * 5)
  local capacity = tonumber(ARGV[offset + 1])
  local refill = tonumber(ARGV[offset + 2])
  local interval_ms = tonumber(ARGV[offset + 3])
  local requested = tonumber(ARGV[offset + 4])
  local idle_ttl_ms = tonumber(ARGV[offset + 5])
  local raw = redis.call('HMGET', KEYS[i], 'tokens', 'updated_ms')
  local has_tokens = raw[1] ~= false
  local has_updated = raw[2] ~= false
  if has_tokens ~= has_updated then
    return redis.error_reply('CORRUPT_COORDINATION_BUCKET')
  end

  local tokens = capacity
  local updated_ms = now_ms
  local clock_delay_ms = 0
  if has_tokens then
    tokens = tonumber(raw[1])
    updated_ms = tonumber(raw[2])
    if not tokens or not updated_ms then
      return redis.error_reply('CORRUPT_COORDINATION_BUCKET')
    end
    if updated_ms < 0 or updated_ms > now_ms + idle_ttl_ms then
      return redis.error_reply('CORRUPT_COORDINATION_BUCKET')
    end
    tokens = math.min(capacity, math.max(0, tokens))
    if now_ms >= updated_ms then
      tokens = math.min(capacity, tokens + ((now_ms - updated_ms) * refill / interval_ms))
      updated_ms = now_ms
    else
      clock_delay_ms = updated_ms - now_ms
    end
  end

  if tokens < requested then
    local wait_ms = clock_delay_ms + math.ceil((requested - tokens) * interval_ms / refill)
    retry_after_ms = math.max(retry_after_ms, math.max(1, wait_ms))
  end
  states[i] = {tokens, updated_ms, requested, idle_ttl_ms + clock_delay_ms}
end

local granted = retry_after_ms == 0
local result = {granted and 1 or 0, now_ms, retry_after_ms}
for i = 1, #KEYS do
  local state = states[i]
  local remaining = state[1]
  if granted then
    remaining = remaining - state[3]
  end
  redis.call('HSET', KEYS[i], 'tokens', tostring(remaining), 'updated_ms', tostring(state[2]))
  redis.call('PEXPIRE', KEYS[i], state[4])
  table.insert(result, math.floor(math.max(0, remaining)))
end
return result
`)
