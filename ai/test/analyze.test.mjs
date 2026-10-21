import { test } from 'node:test';
import assert from 'node:assert';
import {
  normalizeResponseToText,
  extractJsonArtifact,
  isTransientStartupError,
  isStartupRetrySafe,
  validateAnalysis,
  cursorAgentOptions,
} from '../analyze.mjs';

test('isTransientStartupError identifies transient errors', () => {
  assert.strictEqual(isTransientStartupError(new Error('ECONNREFUSED')), true);
  assert.strictEqual(isTransientStartupError(new Error('ENOTFOUND')), true);
  assert.strictEqual(isTransientStartupError(new Error('ETIMEDOUT')), true);
  assert.strictEqual(isTransientStartupError(new Error('Connection timeout')), true);
  assert.strictEqual(isTransientStartupError(new Error('Network error')), true);
  assert.strictEqual(isTransientStartupError(new Error('Connection failed')), true);
  assert.strictEqual(isTransientStartupError(new Error('Service temporarily unavailable')), true);
  assert.strictEqual(isTransientStartupError(new Error('Rate limit exceeded')), true);
});

test('isTransientStartupError rejects non-transient errors', () => {
  assert.strictEqual(isTransientStartupError(new Error('Invalid API key')), false);
  assert.strictEqual(isTransientStartupError(new Error('Permission denied')), false);
  assert.strictEqual(isTransientStartupError(new Error('Not found')), false);
  assert.strictEqual(isTransientStartupError(null), false);
  assert.strictEqual(isTransientStartupError({}), false);
});

test('isStartupRetrySafe does not retry a run that is already streaming', () => {
  const streaming = new Error('socket hang up');
  streaming.operation = 'run.stream';
  assert.strictEqual(isStartupRetrySafe(streaming), false);

  const startup = new Error('ECONNREFUSED');
  assert.strictEqual(isStartupRetrySafe(startup), true);
  assert.strictEqual(isStartupRetrySafe(new Error('Invalid API key')), false);
});

test('validateAnalysis accepts a finding and rejects a bad verdict', () => {
  const analysis = validateAnalysis({
    verdict: 'warning',
    headline: 'CPU is creeping',
    confidence: 'medium',
    findings: [{
      id: 'F1',
      severity: 'medium',
      title: 'Hot path',
      location: { file: 'a.go', line: 10, function: 'pkg.F' },
      fix_prompt: 'Speed up pkg.F',
    }],
  });
  assert.strictEqual(analysis.findings[0].id, 'F1');
  assert.throws(() => validateAnalysis({ verdict: 'maybe' }), /verdict/);
});

test('cursorAgentOptions requires an API key and records the cloud env', () => {
  assert.strictEqual(cursorAgentOptions({}), null);
  const options = cursorAgentOptions({
    CURSOR_API_KEY: 'test-key',
    CURSOR_CLOUD_ENV: 'owner/repo',
    CURSOR_MODEL_PERF: 'composer-2.5',
  });
  assert.strictEqual(options.apiKey, 'test-key');
  assert.strictEqual(options.cloud.env.name, 'owner/repo');
  assert.strictEqual(options.model.id, 'composer-2.5');
});

test('extractJsonArtifact extracts from markdown code block', () => {
  const text = `Here is the analysis:

\`\`\`json
{
  "verdict": "ok",
  "headline": "Test headline",
  "key_numbers": [],
  "notable_changes": [],
  "recommendations": [],
  "confidence": "high"
}
\`\`\`

Done.`;

  const result = extractJsonArtifact(text);
  assert.strictEqual(result.verdict, 'ok');
  assert.strictEqual(result.headline, 'Test headline');
  assert.strictEqual(result.confidence, 'high');
});

test('extractJsonArtifact extracts from plain JSON object', () => {
  const text = `Analysis complete. {"verdict":"regression","headline":"Performance degraded","key_numbers":[],"notable_changes":[],"recommendations":[],"confidence":"medium"}`;

  const result = extractJsonArtifact(text);
  assert.strictEqual(result.verdict, 'regression');
  assert.strictEqual(result.headline, 'Performance degraded');
  assert.strictEqual(result.confidence, 'medium');
});

test('extractJsonArtifact throws on missing JSON', () => {
  const text = 'No JSON here';
  assert.throws(() => extractJsonArtifact(text), /Could not extract JSON artifact/);
});

test('extractJsonArtifact throws on invalid JSON', () => {
  const text = '```json\n{invalid json}\n```';
  assert.throws(() => extractJsonArtifact(text), /JSON/);
});

test('normalizeResponseToText handles string response', () => {
  const response = 'Direct string response';
  const result = normalizeResponseToText(response);
  assert.strictEqual(result, 'Direct string response');
});

test('normalizeResponseToText handles result field', () => {
  const response = { result: 'Response text from result field' };
  const result = normalizeResponseToText(response);
  assert.strictEqual(result, 'Response text from result field');
});

test('normalizeResponseToText handles text field', () => {
  const response = { text: 'Response text from text field' };
  const result = normalizeResponseToText(response);
  assert.strictEqual(result, 'Response text from text field');
});

test('normalizeResponseToText handles content array', () => {
  const response = {
    content: [
      { type: 'text', text: 'First part' },
      { type: 'text', text: 'Second part' },
      { type: 'image', data: 'ignored' }
    ]
  };
  const result = normalizeResponseToText(response);
  assert.strictEqual(result, 'First part\nSecond part');
});

test('normalizeResponseToText handles messages array', () => {
  const response = {
    messages: [
      { text: 'Message 1' },
      { text: 'Message 2' }
    ]
  };
  const result = normalizeResponseToText(response);
  assert.strictEqual(result, 'Message 1\nMessage 2');
});

test('normalizeResponseToText throws on unexpected shape', () => {
  const response = { unexpected: 'field' };
  assert.throws(
    () => normalizeResponseToText(response),
    /Unexpected response shape from Agent.prompt/
  );
});

test('normalizeResponseToText throws on non-object/non-string', () => {
  assert.throws(
    () => normalizeResponseToText(123),
    /Unexpected response type from Agent.prompt/
  );
});
