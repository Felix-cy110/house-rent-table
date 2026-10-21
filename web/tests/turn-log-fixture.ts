export function promptTuningTurn() {
  const input = { rent: '2000', note: '用户输入 🏠 __turn_number_0' };
  const messages = [
    { role: 'system', content: '请核算费用。<script>window.hacked=true</script>' },
    { role: 'developer', content: '不要编造数据。' },
    { role: 'user', content: JSON.stringify(input) },
  ];
  const tools = [{ type: 'function', function: { name: 'calculator', description: '精确计算', parameters: { type: 'object' } } }];
  const request = { model: 'mimo-v2.6-flash', stream: false, thinking: { type: 'enabled' }, messages, tools };
  const assistant = { role: 'assistant', content: null, reasoning_content: '先计算首付款。', tool_calls: [
    { id: 'calc1', type: 'function', function: { name: 'calculator', arguments: '{"value":9007199254740993123,"ratio":0.1234567890123456789012345,"literal":"__turn_number_0"}' } },
  ] };
  const output = '{"amount":"8000.00","id":9007199254740993123,"tiny":1e-400,"zero":-0}';
  const history = [...messages, assistant, { role: 'tool', tool_call_id: 'calc1', content: output }];
  const response = (message: unknown, finish_reason = 'stop') => ({ id: 'vendor-request-id', usage: { total_tokens: 1000 }, vendor_extra: 'full-only', choices: [{ index: 0, finish_reason, message }] });
  const turn = {
    schemaVersion: 1, provider: 'mimo', model: request.model, status: 'completed',
    startedAt: '2026-10-21T00:00:00Z', durationMs: 12500, usage: { total_tokens: 3000 }, input,
    events: [
      { sequence: 1, kind: 'model', method: 'POST', url: 'https://example.com/chat', httpStatus: 200, request, response: response(assistant, 'tool_calls') },
      { sequence: 2, kind: 'tool', name: 'calculator', toolCallId: 'calc1', arguments: assistant.tool_calls[0]!.function.arguments, output },
      { sequence: 3, kind: 'model', request: { ...request, messages: history }, httpStatus: 403, error: '搜索未开通', response: { error: { message: 'Web Search plugin not activated' } } },
      { sequence: 4, kind: 'model', request: { ...request, tools: [], messages: [{ ...messages[0], content: `${messages[0]!.content}\n请使用已有来源完成报告。` }, ...history.slice(1)] }, response: response({ role: 'assistant', reasoning_content: '核对计算结果。', content: '合计 8000.00 元。', annotations: [{ title: '公示', url: 'https://example.com/fees' }] }) },
      { sequence: 5, kind: 'search', request: { query: '租金' }, response: { results: ['重复搜索原文'] }, httpStatus: 200 },
      { sequence: 6, kind: 'search', request: { query: '租金' }, responseText: '搜索服务暂不可用', error: '搜索超时', truncated: true, httpStatus: 502 },
    ],
    result: { text: '合计 8000.00 元。', warnings: ['使用已有来源。'], sources: [{ title: '公示', url: 'https://example.com/fees' }] },
  };
  return JSON.stringify(turn, null, 2);
}
