type Fields = Record<string, unknown>;

const jsonTokens = /"(?:\\.|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g;

class ExactNumber {
  constructor(readonly source: string) {}
}

function unusedPrefix(strings: string[]) {
  let prefix = '__turn_number_';
  while (strings.some(value => value.includes(prefix))) prefix += '_';
  return prefix;
}

// Keep numeric literals intact, including decimals and IDs beyond 2^53. Only
// replace tokens outside strings; reserve markers that cannot collide with data.
function parseExact(json: string): unknown {
  JSON.parse(json); // Validate before replacing tokens, including invalid numbers.
  const strings = [...json.matchAll(jsonTokens)]
    .filter(match => match[0].startsWith('"')).map(match => JSON.parse(match[0]) as string);
  const prefix = unusedPrefix(strings);
  const numbers = new Map<string, ExactNumber>();
  const protectedJSON = json.replace(jsonTokens, token => {
    if (token.startsWith('"') || (token !== '-0' && /^-?\d+$/.test(token) && Number.isSafeInteger(Number(token)))) return token;
    const marker = `${prefix}${numbers.size}`;
    numbers.set(marker, new ExactNumber(token));
    return JSON.stringify(marker);
  });
  return JSON.parse(protectedJSON, (_key, value: unknown) => typeof value === 'string' ? numbers.get(value) ?? value : value);
}

function stringifyExact(value: unknown): string {
  const prefix = unusedPrefix([JSON.stringify(value)]);
  const numbers = new Map<string, string>();
  const json = JSON.stringify(value, (_key, item: unknown) => {
    if (!(item instanceof ExactNumber)) return item;
    const marker = `${prefix}${numbers.size}`;
    numbers.set(marker, item.source);
    return marker;
  }, 2);
  return json.replace(jsonTokens, token => token.startsWith('"') ? numbers.get(JSON.parse(token) as string) ?? token : token);
}

function fields(value: unknown): Fields {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Fields : {};
}

function pick(value: unknown, keys: string[]): Fields {
  const source = fields(value);
  return Object.fromEntries(keys.filter(key => Object.hasOwn(source, key)).map(key => [key, source[key]]));
}

function decodeToolJSON(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  try { return parseExact(value); } catch { return value; }
}

function promptOf(request: Fields): Fields {
  // Assistant/tool messages repeat responses and tool results already in steps.
  const { model: _model, stream: _stream, messages, ...settings } = request;
  return { messages: (messages as Fields[]).filter(message => message.role !== 'assistant' && message.role !== 'tool'), ...settings };
}

function promptChanges(previous: Fields, current: Fields): Fields {
  const changes: Fields = {};
  for (const key of new Set([...Object.keys(previous), ...Object.keys(current)])) {
    if (JSON.stringify(previous[key]) !== JSON.stringify(current[key])) {
      if (key === 'messages') {
        const before = previous.messages as unknown[];
        const after = current.messages as unknown[];
        changes.messages = Array.from({ length: Math.max(before.length, after.length) }, (_, index) => ({ index, message: after[index] ?? null }))
          .filter(({ index, message }) => JSON.stringify(before[index] ?? null) !== JSON.stringify(message));
        continue;
      }
      // null explicitly records removal (e.g. tools disabled at the last round).
      changes[key] = current[key] ?? null;
    }
  }
  return changes;
}

function modelResponse(response: Fields): Fields {
  const output = pick(response, ['error', 'web_search', 'citations']);
  if (Array.isArray(response.choices)) {
    output.choices = response.choices.map(choice => {
      const item = fields(choice);
      const message = pick(item.message, ['role', 'content', 'reasoning_content', 'tool_calls', 'refusal', 'annotations', 'error_message']);
      if (Array.isArray(message.tool_calls)) {
        message.tool_calls = message.tool_calls.map(call => {
          const invocation = fields(call);
          const fn = fields(invocation.function);
          return { ...invocation, function: { ...fn, arguments: decodeToolJSON(fn.arguments) } };
        });
      }
      return { ...pick(item, ['finish_reason']), message };
    });
  }
  return output;
}

/** A prompt-tuning projection of schema v1. Unknown/broken formats stay full. */
export function conciseTurnJSON(json: string): string | null {
  try {
    const turn = fields(parseExact(json));
    if (turn.schemaVersion !== 1 || !Array.isArray(turn.events) || !turn.events.length) return null;
    const steps: Fields[] = [];
    let initialPrompt: Fields | undefined;
    let previousPrompt: Fields = {};
    for (const value of turn.events) {
      const event = fields(value);
      const step = pick(event, ['sequence', 'kind', 'error', 'truncated']);
      switch (event.kind) {
        case 'model': {
          const request = fields(event.request);
          if (!Array.isArray(request.messages) || !request.messages.length ||
              request.messages.some(message => typeof fields(message).role !== 'string')) return null;
          const prompt = promptOf(request);
          if (!initialPrompt) initialPrompt = prompt;
          else {
            const changes = promptChanges(previousPrompt, prompt);
            if (Object.keys(changes).length) step.promptChanges = changes;
          }
          previousPrompt = prompt;
          const output = modelResponse(fields(event.response));
          if (Object.keys(output).length) step.response = output;
          if (event.responseText !== undefined) step.responseText = event.responseText;
          steps.push(step);
          break;
        }
        case 'tool':
          steps.push({ ...step, ...pick(event, ['name', 'toolCallId']), arguments: decodeToolJSON(event.arguments), output: decodeToolJSON(event.output) });
          break;
        case 'search':
          // Successful HTTP search payloads repeat the model-visible tool output.
          // Keep failures/truncation so a fallback does not hide its cause.
          if (event.error || event.truncated || (typeof event.httpStatus === 'number' && event.httpStatus >= 400)) {
            steps.push({ ...step, ...pick(event, ['request', 'response', 'responseText']) });
          }
          break;
        default: return null;
      }
    }
    if (!initialPrompt) return null;
    return stringifyExact({ ...pick(turn, ['provider', 'model', 'status']), prompt: initialPrompt, steps, result: turn.result });
  } catch { return null; }
}
