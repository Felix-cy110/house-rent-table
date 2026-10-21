import { expect, test } from '@playwright/test';
import { conciseTurnJSON } from '../src/turn-log-summary';
import { promptTuningTurn } from './turn-log-fixture';

test('精简日志保留提示词、输入、思考和工具结果，去掉重复历史及传输元数据', () => {
  const full = JSON.parse(promptTuningTurn());
  const json = conciseTurnJSON(promptTuningTurn())!;
  const concise = JSON.parse(json);
  expect(concise.prompt.messages).toEqual(full.events[0].request.messages);
  expect(concise.prompt.tools).toEqual(full.events[0].request.tools);
  expect(concise.prompt.thinking).toEqual({ type: 'enabled' });
  expect(concise.steps.map((step: { kind: string }) => step.kind)).toEqual(['model', 'tool', 'model', 'model', 'search']);
  expect(concise.steps[0].response.choices[0].message.reasoning_content).toBe('先计算首付款。');
  expect(concise.steps[1].output.amount).toBe('8000.00');
  expect(concise.steps[2].promptChanges).toBeUndefined();
  expect(concise.steps[2].response.error.message).toBe('Web Search plugin not activated');
  expect(concise.steps[3].promptChanges).toEqual({ messages: [{ index: 0, message: full.events[3].request.messages[0] }], tools: [] });
  expect(concise.steps[3].response.choices[0].message.annotations).toEqual(full.events[3].response.choices[0].message.annotations);
  expect(concise.steps[4]).toMatchObject({ error: '搜索超时', truncated: true, responseText: '搜索服务暂不可用' });
  expect(concise.result).toEqual(full.result);
  expect(json.match(/用户输入/g)).toHaveLength(1);
  expect(json.match(/先计算首付款/g)).toHaveLength(1);
  for (const field of ['httpStatus', 'startedAt', 'durationMs', 'usage', 'vendor_extra', '重复搜索原文', 'https://example.com/chat']) expect(json).not.toContain(field);
  expect(json.length).toBeLessThan(promptTuningTurn().length);
});

test('精简日志解析嵌套工具 JSON 时保留大整数、小数、字符串和原始异常文本', () => {
  const json = conciseTurnJSON(promptTuningTurn())!;
  expect(json).toContain('"id": 9007199254740993123');
  expect(json).toContain('"value": 9007199254740993123');
  expect(json).toContain('"ratio": 0.1234567890123456789012345');
  expect(json).toContain('"tiny": 1e-400');
  expect(json).toContain('"zero": -0');
  expect(JSON.parse(json).steps[1].arguments.literal).toBe('__turn_number_0');
  const full = JSON.parse(promptTuningTurn());
  full.events[1].arguments = '{"invalid":01}';
  full.events[1].output = '工具返回非 JSON 文本';
  expect(JSON.parse(conciseTurnJSON(JSON.stringify(full))!).steps[1]).toMatchObject({ arguments: '{"invalid":01}', output: '工具返回非 JSON 文本' });
});

test('格式未知或损坏时不给出不完整的精简日志，工具移除仍有记录', () => {
  const full = JSON.parse(promptTuningTurn());
  delete full.events[3].request.tools;
  expect(JSON.parse(conciseTurnJSON(JSON.stringify(full))!).steps[3].promptChanges.tools).toBeNull();
  for (const value of ['{invalid', '{}', 'null', '{"schemaVersion":2,"events":[]}', '{"schemaVersion":1,"events":[null]}']) {
    expect(conciseTurnJSON(value)).toBeNull();
  }
  full.events[0].request.messages = [null];
  expect(conciseTurnJSON(JSON.stringify(full))).toBeNull();
});
