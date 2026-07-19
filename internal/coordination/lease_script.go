package coordination

import "github.com/redis/go-redis/v9"

const leaseKeyGraceMilliseconds = int64(1000)

var acquireLeaseScript = redis.NewScript(`
local clock = redis.call('TIME')
local now_ms = (tonumber(clock[1]) * 1000) + math.floor(tonumber(clock[2]) / 1000)
local ttl_ms = tonumber(ARGV[1])
local member = ARGV[2]
local grace_ms = tonumber(ARGV[3])
local expires_at_ms = now_ms + ttl_ms
local counts = {}
local retry_after_ms = 0

local function retain_live_key(key)
  local tail = redis.call('ZREVRANGE', key, 0, 0, 'WITHSCORES')
  if #tail == 0 then
    redis.call('DEL', key)
  else
    redis.call('PEXPIREAT', key, math.floor(tonumber(tail[2])) + grace_ms)
  end
end

for i = 1, #KEYS do
  local limit = tonumber(ARGV[3 + i])
  redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now_ms)
  local exists = redis.call('ZSCORE', KEYS[i], member) ~= false
  local count = redis.call('ZCARD', KEYS[i])
  counts[i] = count
  if not exists and count >= limit then
    local blocking_index = count - limit
    local blocking = redis.call('ZRANGE', KEYS[i], blocking_index, blocking_index, 'WITHSCORES')
    if #blocking ~= 2 then
      return redis.error_reply('CORRUPT_COORDINATION_LEASE')
    end
    retry_after_ms = math.max(retry_after_ms, math.max(1, math.ceil(tonumber(blocking[2]) - now_ms)))
  end
end

local granted = retry_after_ms == 0
local result = {granted and 1 or 0, now_ms, granted and expires_at_ms or retry_after_ms}
if granted then
  for i = 1, #KEYS do
    local existed = redis.call('ZSCORE', KEYS[i], member) ~= false
    redis.call('ZADD', KEYS[i], expires_at_ms, member)
    if not existed then
      counts[i] = counts[i] + 1
    end
    retain_live_key(KEYS[i])
  end
else
  for i = 1, #KEYS do
    retain_live_key(KEYS[i])
  end
end
for i = 1, #KEYS do
  table.insert(result, counts[i])
end
return result
`)

var renewLeaseScript = redis.NewScript(`
local clock = redis.call('TIME')
local now_ms = (tonumber(clock[1]) * 1000) + math.floor(tonumber(clock[2]) / 1000)
local ttl_ms = tonumber(ARGV[1])
local member = ARGV[2]
local grace_ms = tonumber(ARGV[3])
local expires_at_ms = now_ms + ttl_ms

local function retain_live_key(key)
  local tail = redis.call('ZREVRANGE', key, 0, 0, 'WITHSCORES')
  if #tail == 0 then
    redis.call('DEL', key)
  else
    redis.call('PEXPIREAT', key, math.floor(tonumber(tail[2])) + grace_ms)
  end
end

local held = true
for i = 1, #KEYS do
  redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now_ms)
  if redis.call('ZSCORE', KEYS[i], member) == false then
    held = false
  end
end
if not held then
  for i = 1, #KEYS do
    redis.call('ZREM', KEYS[i], member)
    retain_live_key(KEYS[i])
  end
  return {0, now_ms, 0}
end
for i = 1, #KEYS do
  redis.call('ZADD', KEYS[i], expires_at_ms, member)
  retain_live_key(KEYS[i])
end
return {1, now_ms, expires_at_ms}
`)

var releaseLeaseScript = redis.NewScript(`
local clock = redis.call('TIME')
local now_ms = (tonumber(clock[1]) * 1000) + math.floor(tonumber(clock[2]) / 1000)
local member = ARGV[1]
local grace_ms = tonumber(ARGV[2])
local removed = 0

for i = 1, #KEYS do
  redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now_ms)
  removed = removed + redis.call('ZREM', KEYS[i], member)
  local tail = redis.call('ZREVRANGE', KEYS[i], 0, 0, 'WITHSCORES')
  if #tail == 0 then
    redis.call('DEL', KEYS[i])
  else
    redis.call('PEXPIREAT', KEYS[i], math.floor(tonumber(tail[2])) + grace_ms)
  end
end
return {now_ms, removed}
`)

var cleanupLeaseScript = redis.NewScript(`
local clock = redis.call('TIME')
local now_ms = (tonumber(clock[1]) * 1000) + math.floor(tonumber(clock[2]) / 1000)
local grace_ms = tonumber(ARGV[1])
local result = {now_ms, 0}

for i = 1, #KEYS do
  local removed = redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now_ms)
  result[2] = result[2] + removed
  local count = redis.call('ZCARD', KEYS[i])
  local tail = redis.call('ZREVRANGE', KEYS[i], 0, 0, 'WITHSCORES')
  if #tail == 0 then
    redis.call('DEL', KEYS[i])
  else
    redis.call('PEXPIREAT', KEYS[i], math.floor(tonumber(tail[2])) + grace_ms)
  end
  table.insert(result, count)
end
return result
`)
