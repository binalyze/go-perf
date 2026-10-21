#!/usr/bin/env node
import { Agent, AuthenticationError, ConfigurationError, NetworkError, RateLimitError } from '@cursor/sdk';
import { readFile, writeFile } from 'fs/promises';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));

const MAX_RETRIES = 3;
const RETRY_DELAYS = [2000, 4000];

const TRANSIENT_STARTUP = /ECONNREFUSED|ENOTFOUND|ETIMEDOUT|econnreset|eai_again|socket hang up|timeout|network|connection.*failed|temporarily unavailable|rate limit|fetch failed/i;
const NON_TRANSIENT_STARTUP = /invalid model|model .*not found|unauthorized|unauthenticated|invalid api key|\b401\b|\b403\b|permission denied/i;
const IN_FLIGHT = /run\.(wait|stream|conversation)|\/stream/i;

const VERDICTS = new Set(['ok', 'warning', 'regression']);
const CONFIDENCE = new Set(['high', 'medium', 'low']);
const SEVERITIES = new Set(['high', 'medium', 'low']);

export function isTransientStartupError(error) {
  if (!error || typeof error !== 'object') return false;
  if (error instanceof AuthenticationError || error instanceof ConfigurationError) return false;
  const message = errorBlob(error);
  if (NON_TRANSIENT_STARTUP.test(message)) return false;
  if (error instanceof NetworkError || error instanceof RateLimitError) return true;
  const status = error.status ?? error.cause?.status;
  if (status === 429 || (typeof status === 'number' && status >= 500 && status <= 599)) return true;
  return TRANSIENT_STARTUP.test(message);
}

// isStartupRetrySafe is false once the cloud run exists. Retrying then would
// start a second agent.
export function isStartupRetrySafe(error) {
  if (!isTransientStartupError(error)) return false;
  const operation = error.operation || error.cause?.operation || '';
  const endpoint = String(error.endpoint || error.cause?.endpoint || '');
  return !IN_FLIGHT.test(`${operation} ${endpoint}`);
}

function errorBlob(error) {
  return [error.message, error.code, error.cause?.message, error.cause?.code].filter(Boolean).join(' ');
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function addJitter(ms, maxJitterMs = 500) {
  return ms + Math.random() * maxJitterMs;
}

export async function callAgentWithRetry(prompt, options) {
  let lastError;
  for (let attempt = 0; attempt < MAX_RETRIES; attempt++) {
    try {
      return await Agent.prompt(prompt, options);
    } catch (error) {
      lastError = error;
      if (!isStartupRetrySafe(error) || attempt === MAX_RETRIES - 1) {
        throw error;
      }
      const delay = RETRY_DELAYS[Math.min(attempt, RETRY_DELAYS.length - 1)];
      const jitteredDelay = addJitter(delay);
      console.error(`Transient error on attempt ${attempt + 1}/${MAX_RETRIES}: ${error.message}`);
      console.error(`Retrying in ${Math.round(jitteredDelay)}ms...`);
      await sleep(jitteredDelay);
    }
  }
  throw lastError;
}

export function resolveModel(env = process.env) {
  return env.CURSOR_MODEL_PERF || env.CURSOR_MODEL || 'composer-2.5';
}

// cursorAgentOptions follows the cloud-environment shape used by the
// vulnerability fixer: the named cloud env is the repository the agent reads.
export function cursorAgentOptions(env = process.env) {
  const apiKey = env.CURSOR_API_KEY?.trim();
  if (!apiKey) return null;
  const cloudEnv = env.CURSOR_CLOUD_ENV?.trim();
  const options = {
    apiKey,
    model: { id: resolveModel(env) },
  };
  if (cloudEnv) {
    options.cloud = { env: { type: 'cloud', name: cloudEnv } };
  }
  return options;
}

export function normalizeResponseToText(response) {
  if (typeof response === 'string') {
    return response;
  }

  if (response && typeof response === 'object') {
    if (typeof response.result === 'string') {
      return response.result;
    }
    if (typeof response.text === 'string') {
      return response.text;
    }
    if (Array.isArray(response.content)) {
      const textParts = response.content
        .filter(c => c.type === 'text' && typeof c.text === 'string')
        .map(c => c.text);
      if (textParts.length > 0) {
        return textParts.join('\n');
      }
    }
    if (Array.isArray(response.messages)) {
      const textParts = response.messages
        .filter(m => m.text && typeof m.text === 'string')
        .map(m => m.text);
      if (textParts.length > 0) {
        return textParts.join('\n');
      }
    }
    throw new Error(`Unexpected response shape from Agent.prompt. Expected string or object with result/text/content/messages, got: ${JSON.stringify(Object.keys(response))}`);
  }

  throw new Error(`Unexpected response type from Agent.prompt: ${typeof response}`);
}

export function extractJsonArtifact(text) {
  const jsonBlockRegex = /```json\s*\n([\s\S]*?)\n```/;
  const match = text.match(jsonBlockRegex);

  if (match) {
    return JSON.parse(match[1]);
  }

  const jsonObjectRegex = /(\{[\s\S]*"verdict"[\s\S]*\})/;
  const objMatch = text.match(jsonObjectRegex);

  if (objMatch) {
    return JSON.parse(objMatch[1]);
  }

  throw new Error('Could not extract JSON artifact from response');
}

export function validateAnalysis(analysis) {
  if (!analysis || typeof analysis !== 'object' || Array.isArray(analysis)) {
    throw new Error('AI analysis must be a JSON object');
  }
  if (!VERDICTS.has(analysis.verdict)) {
    throw new Error(`verdict must be ok, warning, or regression, got ${analysis.verdict}`);
  }
  if (analysis.confidence !== undefined && !CONFIDENCE.has(analysis.confidence)) {
    throw new Error(`confidence must be high, medium, or low, got ${analysis.confidence}`);
  }
  if (!Array.isArray(analysis.findings)) analysis.findings = [];
  if (!Array.isArray(analysis.metric_notes)) analysis.metric_notes = [];
  if (!Array.isArray(analysis.recommendations)) analysis.recommendations = [];
  for (const finding of analysis.findings) {
    if (!finding || typeof finding !== 'object') {
      throw new Error('each finding must be an object');
    }
    if (!finding.id || !finding.title) {
      throw new Error('each finding needs an id and a title');
    }
    if (!SEVERITIES.has(finding.severity)) {
      throw new Error(`finding ${finding.id} has severity ${finding.severity}`);
    }
    if (!finding.location || typeof finding.location !== 'object') {
      finding.location = {};
    }
  }
  return analysis;
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length !== 2) {
    console.error('Usage: node analyze.mjs <ai-input.json> <ai-analysis.json>');
    process.exit(1);
  }

  const [inputPath, outputPath] = args;
  const options = cursorAgentOptions();
  if (!options) {
    console.error('Error: CURSOR_API_KEY environment variable is required');
    process.exit(1);
  }
  if (!options.cloud) {
    console.error('Error: CURSOR_CLOUD_ENV is required (for example owner/repo) so the agent can read the source');
    process.exit(1);
  }

  console.log('Reading AI input...');
  const aiInput = JSON.parse(await readFile(inputPath, 'utf-8'));

  console.log('Loading prompt template...');
  const promptTemplate = await readFile(resolve(__dirname, 'prompts/analysis.md'), 'utf-8');
  const prompt = promptTemplate.replace('{{AI_INPUT_JSON}}', JSON.stringify(aiInput, null, 2));

  console.log(`Calling Cursor Agent (model: ${options.model.id}, cloud: ${options.cloud.env.name})...`);
  const response = await callAgentWithRetry(prompt, options);

  console.log('Parsing AI response...');
  const responseText = normalizeResponseToText(response);
  if (!responseText || responseText.trim() === '') {
    throw new Error('Agent.prompt returned empty text');
  }

  const analysis = validateAnalysis(extractJsonArtifact(responseText));
  console.log(`Writing AI analysis to ${outputPath}...`);
  await writeFile(outputPath, JSON.stringify(analysis, null, 2));

  console.log('AI analysis complete!');
  console.log(`Verdict: ${analysis.verdict}`);
  console.log(`Headline: ${analysis.headline}`);
  console.log(`Findings: ${analysis.findings.length}`);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch(error => {
    console.error('Fatal error:', error.message);
    if (error.stack) {
      console.error(error.stack);
    }
    process.exit(1);
  });
}
