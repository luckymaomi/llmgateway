window.adminViews = {
  "admin-dashboard": {
    title: "仪表盘",
    content: `
      <div class="page-head"><div><h1>运营总览</h1><span class="muted">最近 24 小时</span></div><button class="button" data-refresh>刷新</button></div>
      <div class="metrics"><article><span>请求</span><strong>80,923</strong><small>成功率 99.81%</small></article><article><span>Token</span><strong>42.8M</strong><small>输入 36.2M · 输出 6.6M</small></article><article><span>上游 API Key</span><strong>5 / 7</strong><small>1 个冷却 · 1 个停用</small></article><article><span>成员</span><strong>286</strong><small>3 人待审核</small></article></div>
      <div class="admin-grid"><article class="panel"><div class="panel-head"><strong>请求趋势</strong><span class="badge positive">实时</span></div><div class="bar-chart" aria-label="24 小时请求趋势">${bars([28, 35, 31, 46, 58, 52, 71, 62, 80, 67, 76, 92])}</div><div class="chart-axis"><span>00:00</span><span>12:00</span><span>现在</span></div></article><article class="panel"><div class="panel-head"><strong>当前运行状态</strong><button class="text-button" data-view-link="operations">查看运维</button></div><div class="fact-list"><div><span>首字节 P95</span><b>162 ms</b></div><div><span>总延迟 P95</span><b>1.06 s</b></div><div><span>待确认请求</span><b class="warning-text">4</b></div><div><span>待审核成员</span><b>3</b></div></div></article></div>
      <article class="panel"><div class="panel-head"><strong>首次运营进度</strong><span class="muted">7 / 8</span></div><div class="check-grid"><span>✓ Provider</span><span>✓ 上游 API Key</span><span>✓ 模型价格</span><span>✓ 配置发布</span><span>✓ 成员</span><span>✓ 额度</span><span>✓ Gateway Key</span><span class="pending">○ 首条真实请求</span></div></article>`,
  },
  operations: {
    title: "运维监控",
    content: `
      <div class="page-head"><h1>运维监控</h1><div class="head-actions"><select><option>最近 24 小时</option><option>最近 7 天</option></select><button class="button" data-refresh>刷新</button></div></div>
      <div class="metrics"><article><span>成功率</span><strong>99.81%</strong><small>80,769 / 80,923</small></article><article><span>请求 P95</span><strong>1.06 s</strong><small>首字节 162 ms</small></article><article><span>活跃流</span><strong>42</strong><small>并发峰值 60</small></article><article><span>错误</span><strong>154</strong><small>429 为 119 次</small></article></div>
      <article class="panel table-panel"><div class="panel-head"><strong>上游 API Key 健康</strong><span class="muted">按真实 attempt 派生</span></div>${table(
        ["API Key", "Provider", "24 小时成功率", "P95", "状态", "最近错误"],
        [
          [
            "agnes-main",
            "Agnes",
            "99.9%",
            "921 ms",
            status("可用", "positive"),
            "—",
          ],
          [
            "glm-primary",
            "智谱 GLM",
            "98.7%",
            "1.42 s",
            status("可用", "positive"),
            "upstream_rate_limited",
          ],
          [
            "gemini-a",
            "Google Gemini",
            "91.3%",
            "3.06 s",
            status("冷却中", "warning"),
            "upstream_unavailable",
          ],
          [
            "siliconflow",
            "硅基流动",
            "99.6%",
            "1.08 s",
            status("可用", "positive"),
            "—",
          ],
        ],
      )}</article>
      <div class="admin-grid"><article class="panel"><div class="panel-head"><strong>错误分布</strong></div><div class="rank-list"><div><span>upstream_rate_limited</span><b>119</b></div><div><span>client_canceled</span><b>21</b></div><div><span>upstream_unavailable</span><b>10</b></div><div><span>upstream_outcome_uncertain</span><b>4</b></div></div></article><article class="panel"><div class="panel-head"><strong>基础设施</strong></div><div class="fact-list"><div><span>PostgreSQL 连接</span><b>31 / 48</b></div><div><span>Valkey P95</span><b>1.13 ms</b></div><div><span>Gateway 实例</span><b>2 / 2</b></div><div><span>恢复队列</span><b>0</b></div></div></article></div>`,
  },
  providers: listView(
    "Provider",
    "添加 Provider",
    ["名称", "类型", "模型", "API Key", "状态", "最近核验"],
    [
      ["Agnes", "agnes", "1", "2", status("已启用", "positive"), "今天 21:10"],
      [
        "智谱 GLM",
        "zhipu",
        "1",
        "1",
        status("已启用", "positive"),
        "今天 21:08",
      ],
      [
        "Google Gemini",
        "gemini",
        "1",
        "1",
        status("已启用", "positive"),
        "今天 20:52",
      ],
      [
        "硅基流动",
        "openai-compatible",
        "1",
        "1",
        status("已启用", "positive"),
        "今天 20:47",
      ],
    ],
  ),
  models: listView(
    "模型",
    "添加模型",
    ["网关别名", "上游模型", "Provider", "能力", "上下文", "状态"],
    [
      [
        "agnes-flash",
        "agnes-2.0-flash",
        "Agnes",
        "流式 · 工具 · 推理",
        "128K",
        status("可用", "positive"),
      ],
      [
        "glm-5.2",
        "glm-5.2",
        "智谱 GLM",
        "流式 · 工具 · 推理",
        "128K",
        status("可用", "positive"),
      ],
      [
        "gemini-flash",
        "gemini-3.5-flash",
        "Google Gemini",
        "流式 · 工具 · 推理",
        "未知",
        status("可用", "positive"),
      ],
      [
        "qwen-9b",
        "Qwen/Qwen3.5-9B",
        "硅基流动",
        "流式 · 工具 · 推理",
        "256K",
        status("可用", "positive"),
      ],
    ],
  ),
  credentials: {
    title: "上游 API Key",
    content: `
      <div class="page-head"><h1>上游 API Key</h1><button class="button" data-action>添加 API Key</button></div>
      <div class="metrics three"><article><span>可用</span><strong>5</strong><small>可参与路由</small></article><article><span>冷却中</span><strong>1</strong><small>13:00 后自动恢复</small></article><article><span>停用</span><strong>1</strong><small>不会接收请求</small></article></div>
      <article class="panel table-panel"><div class="toolbar"><input aria-label="搜索上游 API Key" placeholder="搜索名称或 Provider"><select><option>全部状态</option><option>可用</option><option>冷却中</option><option>停用</option></select></div><div class="table-wrap"><table><thead><tr><th>名称</th><th>Provider</th><th>模型路由</th><th>24 小时成功率</th><th>状态</th><th>最近测试</th><th>操作</th></tr></thead><tbody>
        <tr><td><strong>agnes-main</strong></td><td>Agnes</td><td>agnes-flash</td><td>99.9%</td><td>${status("可用", "positive")}</td><td>21:10 成功</td><td><button class="text-button" data-probe="agnes-main" data-model="agnes-flash">测试连接</button></td></tr>
        <tr><td><strong>glm-primary</strong></td><td>智谱 GLM</td><td>glm-5.2</td><td>98.7%</td><td>${status("可用", "positive")}</td><td>21:08 成功</td><td><button class="text-button" data-probe="glm-primary" data-model="glm-5.2">测试连接</button></td></tr>
        <tr><td><strong>gemini-a</strong></td><td>Google Gemini</td><td>gemini-flash</td><td>91.3%</td><td>${status("冷却中", "warning")}</td><td>20:52 · 503</td><td><button class="text-button" data-probe="gemini-a" data-model="gemini-flash">查看状态</button></td></tr>
        <tr><td><strong>siliconflow</strong></td><td>硅基流动</td><td>qwen-9b</td><td>99.6%</td><td>${status("可用", "positive")}</td><td>20:47 成功</td><td><button class="text-button" data-probe="siliconflow" data-model="qwen-9b">测试连接</button></td></tr>
      </tbody></table></div></article>`,
  },
  revisions: listView(
    "配置发布",
    "捕获当前配置",
    ["版本", "状态", "Provider", "模型", "API Key", "创建时间"],
    [
      ["rev-018", status("生效中", "positive"), "4", "4", "5", "今天 20:40"],
      ["rev-017", status("已验证", "neutral"), "4", "4", "5", "今天 18:12"],
      ["rev-016", status("历史版本", "neutral"), "3", "3", "4", "昨天 09:30"],
    ],
  ),
  members: listView(
    "成员",
    "创建邀请",
    ["成员", "状态", "模型授权", "Key", "剩余额度", "最近活动"],
    [
      ["陈晨", status("可用", "positive"), "4", "2", "6.2M", "2 分钟前"],
      ["李明", status("待审核", "warning"), "0", "0", "0", "尚未登录"],
      ["王宁", status("可用", "positive"), "2", "1", "1.8M", "1 小时前"],
      ["赵文", status("已停用", "danger"), "3", "1", "420K", "3 天前"],
    ],
  ),
  invitations: listView(
    "邀请",
    "创建邀请",
    ["邀请码", "状态", "领取人", "到期", "创建人"],
    [
      [
        "invite_8fd2…",
        status("已签发", "positive"),
        "尚未领取",
        "07-29 21:00",
        "管理员",
      ],
      [
        "invite_4a11…",
        status("已领取", "neutral"),
        "李明",
        "07-28 10:20",
        "管理员",
      ],
    ],
  ),
  "gateway-keys": listView(
    "Gateway Key",
    "创建 Key",
    ["名称", "所属成员", "Key", "模型", "状态", "最近使用"],
    [
      [
        "工作站",
        "陈晨",
        "llmgw_72a9…",
        "3 个",
        status("可用", "positive"),
        "2 分钟前",
      ],
      [
        "CI Agent",
        "陈晨",
        "llmgw_c81f…",
        "2 个",
        status("可用", "positive"),
        "1 小时前",
      ],
      [
        "数据分析",
        "王宁",
        "llmgw_a201…",
        "1 个",
        status("可用", "positive"),
        "昨天 19:32",
      ],
    ],
  ),
  entitlements: listView(
    "订阅与额度",
    "分配额度",
    ["成员", "计划", "资源域", "模型", "可用 / 发放", "并发", "到期"],
    [
      ["陈晨", "Token Plan", "专业", "qwen-9b", "3.8M / 5M", "4", "07-31"],
      ["陈晨", "Coding Plan", "免费", "全部模型", "2.4M / 3M", "8", "08-15"],
      ["王宁", "Token Plan", "专业", "glm-5.2", "1.8M / 2M", "2", "07-30"],
    ],
  ),
  "admin-requests": {
    title: "API 日志",
    content: `
      <div class="page-head"><h1>API 日志</h1><button class="button" data-refresh>刷新</button></div>
      <article class="panel table-panel"><div class="request-toolbar"><input aria-label="搜索请求" placeholder="成员、Key 或 Request ID"><select aria-label="时间范围"><option>最近 24 小时</option><option>最近 7 天</option></select><select aria-label="筛选模型"><option>全部模型</option><option>qwen-9b</option><option>glm-5.2</option><option>gemini-flash</option></select><select aria-label="筛选状态" data-request-status><option value="all">全部状态</option><option value="completed">已完成</option><option value="failed">失败</option><option value="uncertain">待确认</option></select></div><div class="table-wrap"><table><thead><tr><th>时间</th><th>成员 / Key</th><th>模型</th><th>Token</th><th>首字节 / 总耗时</th><th>状态</th><th>Request ID</th><th></th></tr></thead><tbody>
        <tr data-request-row="completed"><td>21:34:32</td><td><strong>陈晨</strong><small class="cell-note">工作站</small></td><td>qwen-9b</td><td>1,540 / 331</td><td>148 ms / 842 ms</td><td>${status("已完成", "positive")}</td><td><code>6c34…</code></td><td><button class="text-button" data-request-detail="6c34" data-state="已完成">详情</button></td></tr>
        <tr data-request-row="uncertain"><td>21:33:52</td><td><strong>王宁</strong><small class="cell-note">数据分析</small></td><td>glm-5.2</td><td>未知</td><td>— / 4.00 s</td><td>${status("待确认", "warning")}</td><td><code>ae19…</code></td><td><button class="text-button" data-request-detail="ae19" data-state="待确认">详情</button></td></tr>
        <tr data-request-row="failed"><td>21:31:14</td><td><strong>陈晨</strong><small class="cell-note">CI Agent</small></td><td>gemini-flash</td><td>0 / 0</td><td>— / 213 ms</td><td>${status("失败", "danger")}</td><td><code>b844…</code></td><td><button class="text-button" data-request-detail="b844" data-state="失败">详情</button></td></tr>
      </tbody></table></div><div class="table-footer"><span class="muted" data-request-count>3 条记录</span><span class="muted">第 1 / 1 页</span></div></article>`,
  },
  ledger: listView(
    "额度记录",
    "刷新",
    ["时间", "成员", "事件", "Token 变化", "资源域", "原因"],
    [
      ["21:34:32", "陈晨", "结算", "-1,871", "专业", "请求 6c34…"],
      ["20:00:00", "王宁", "发放", "+2,000,000", "专业", "项目额度"],
      ["19:42:13", "陈晨", "释放", "+4,096", "免费", "请求取消"],
    ],
  ),
  costs: listView(
    "上游成本",
    "新增价格",
    ["模型", "Provider", "请求", "输入 Token", "输出 Token", "采购成本"],
    [
      ["qwen-9b", "硅基流动", "42,130", "21.4M", "4.1M", "CNY 28.42"],
      ["glm-5.2", "智谱 GLM", "18,206", "8.2M", "1.7M", "CNY 63.18"],
      ["agnes-flash", "Agnes", "12,448", "4.9M", "620K", "USD 4.71"],
    ],
  ),
  settings: {
    title: "站点设置",
    content: `<div class="page-head"><h1>站点设置</h1></div><article class="panel settings-panel"><div class="panel-head"><strong>站点资料</strong></div><form class="form-grid"><label>站点名称<input value="LLMGateway"></label><label>联系信息<input value="ops@example.com"></label><label class="full">简短说明<input value="团队统一大模型入口"></label><div class="full form-actions"><button type="button" class="button" data-save>保存</button></div></form></article>`,
  },
};

function listView(title, action, headers, rows) {
  return {
    title,
    content: `<div class="page-head"><h1>${title}</h1><button class="button" data-action>${action}</button></div><article class="panel table-panel"><div class="toolbar"><input aria-label="搜索" placeholder="搜索"><select><option>全部状态</option><option>可用</option><option>异常</option></select></div>${table(headers, rows)}</article>`,
  };
}
function table(headers, rows) {
  return `<div class="table-wrap"><table><thead><tr>${headers.map((item) => `<th>${item}</th>`).join("")}</tr></thead><tbody>${rows.map((row) => `<tr>${row.map((item) => `<td>${item}</td>`).join("")}</tr>`).join("")}</tbody></table></div>`;
}
function status(label, tone) {
  return `<span class="badge ${tone === "neutral" ? "" : tone}">${label}</span>`;
}
function bars(values) {
  return values.map((value) => `<i style="height:${value}%"></i>`).join("");
}
