const probeErrors: Record<string, string> = {
  authentication: 'API Key 无效',
  permission: '上游拒绝访问',
  quota: '上游额度不足',
  rate_limit: '上游请求过多',
  invalid_request: '模型请求不被上游接受',
  provider_configuration: '模型或接口配置不正确',
  provider_temporary: '上游服务暂时不可用',
  provider_permanent: '上游拒绝该请求',
  unsupported_capability: '模型不支持该测试请求',
  dns_resolution_failed: '无法解析上游地址',
  outbound_address_blocked: '上游地址未通过安全检查',
  upstream_connection_failed: '无法连接上游服务',
  tls_handshake_failed: '上游 HTTPS 证书校验失败',
  provider_transport_failed: '上游网络传输失败',
  probe_timeout_or_canceled: '等待上游响应超时或已取消',
  probe_response_too_large: '上游响应超过安全上限',
  uncertain: '请求结果无法确认',
}

export function probeErrorLabel(kind: string | undefined): string {
  if (!kind) return '未知错误'
  return probeErrors[kind] ?? kind
}
