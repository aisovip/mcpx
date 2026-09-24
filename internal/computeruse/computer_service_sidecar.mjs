#!/usr/bin/env node

// MCPX-owned adapter. The persistent cua API behavior follows the MIT-licensed
// iFurySt/open-codex-computer-use Node REPL contract at commit 51f3a59 without
// depending on that repository at runtime.

import { Buffer } from "node:buffer";
import { spawn } from "node:child_process";
import { PassThrough } from "node:stream";
import { Worker, isMainThread, parentPort, workerData } from "node:worker_threads";
import * as replModule from "node:repl";
import util from "node:util";
import readline from "node:readline";

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_TIMEOUT_MS = 300_000;
const NATIVE_REQUEST_TIMEOUT_MS = 120_000;
const DEFAULT_NATIVE_DRAG_SEGMENTS = 256;
const DEFAULT_FALLBACK_DRAG_SEGMENTS = 24;
const MAX_DRAG_SEGMENTS = 256;
const MAX_FALLBACK_DRAG_SEGMENTS = 64;
const DEFAULT_DRAG_DURATION_MS = 800;
const MIN_DRAG_DURATION_MS = 50;
const MAX_DRAG_DURATION_MS = 10_000;
const MAX_DELAY_MS = 1_000;
const MAX_KEY_SEQUENCE_STEPS = 256;
const CUA_GUIDANCE = `Computer Use JavaScript API:
- await cua.listApps({ emit?: false })
- const app = await cua.getApp("App Name")
- await app.getAXState({ textLimit?, maxTreeNodes?, maxTreeDepth? })
- await app.getScreenshot()
- await app.click(elementIndex | [x, y], { mouseButton?, clickCount?, clickMethod? })
- await app.typeText(text)
- await app.pressKey(key)
- await app.setValue(elementIndex, value)
- await app.scroll(elementIndex, direction, pages?)
- await app.drag([fromX, fromY], [toX, toY])
- await app.dragPath({ points: [[x, y], ...], durationMs?, mouseButton?, delivery? })
- await app.dragPath({ from, to, curve, steps?, durationMs?, mouseButton?, delivery? })
- await app.hotkey("cmd", "shift", "p")
- await app.keySequence([{ key: "cmd+a" }, { text: "hello" }, { key: "Return" }], { delayMs? })
- await app.performSecondaryAction(elementIndex, action)`;

async function runServer(binaryPath) {
  if (typeof binaryPath !== "string" || binaryPath.trim() === "") {
    throw new Error("open-computer-use binary path is required");
  }
  const native = new NativePeer(binaryPath.trim());
  await native.initialize();
  const session = new WorkerJavaScriptSession(native, native.toolNames);
  const server = new MCPStdioServer(session);

  const shutdown = async () => {
    await session.close().catch(() => {});
    native.close();
  };
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    process.on(signal, () => {
      void shutdown().finally(() => process.exit(0));
    });
  }
  try {
    await server.run();
  } finally {
    await shutdown();
  }
}

class MCPStdioServer {
  constructor(session) {
    this.session = session;
    this.queue = Promise.resolve();
  }

  async run() {
    const lines = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
    for await (const line of lines) {
      if (!line.trim()) continue;
      this.queue = this.queue.then(() => this.handleLine(line)).catch((error) => {
        process.stderr.write(`mcpx-computer-use adapter: ${error.message}\n`);
      });
      await this.queue;
    }
  }

  send(message) {
    process.stdout.write(`${JSON.stringify(message)}\n`);
  }

  async handleLine(line) {
    let request;
    try {
      request = JSON.parse(line);
    } catch {
      this.send({ jsonrpc: "2.0", id: null, error: { code: -32700, message: "invalid JSON-RPC payload" } });
      return;
    }
    const { id, method, params = {} } = request;
    if (id === undefined) return;
    try {
      if (method === "initialize") {
        this.send({ jsonrpc: "2.0", id, result: {
          protocolVersion: "2025-06-18",
          capabilities: { tools: { listChanged: false } },
          serverInfo: { name: "mcpx-computer-use-repl", version: "1" },
          instructions: "Execute JavaScript in a persistent Computer Use REPL using the initialized cua API.",
        } });
        return;
      }
      if (method === "ping") {
        this.send({ jsonrpc: "2.0", id, result: {} });
        return;
      }
      if (method === "tools/list") {
        this.send({ jsonrpc: "2.0", id, result: { tools: toolDefinitions() } });
        return;
      }
      if (method === "tools/call") {
        const toolName = String(params.name ?? "");
        if (toolName === "js_reset") {
          await this.session.reset();
          this.send({ jsonrpc: "2.0", id, result: { content: [{ type: "text", text: "Computer Use JavaScript session reset" }], isError: false } });
          return;
        }
        if (toolName === "js") {
          const timeout = normalizeTimeout(params.arguments?.timeout_ms);
          const result = await this.session.run(String(params.arguments?.code ?? ""), timeout, params._meta ?? {});
          this.send({ jsonrpc: "2.0", id, result });
          return;
        }
        throw new Error(`unsupported tool: ${toolName}`);
      }
      throw new Error(`unsupported method: ${method}`);
    } catch (error) {
      this.send({ jsonrpc: "2.0", id, error: { code: -32603, message: error instanceof Error ? error.message : String(error) } });
    }
  }
}

function toolDefinitions() {
  return [
    {
      name: "js",
      description: "Run JavaScript in a persistent Computer Use REPL with top-level await and the initialized cua API.",
      annotations: { destructiveHint: true, openWorldHint: true },
      inputSchema: {
        type: "object",
        additionalProperties: false,
        properties: {
          code: { type: "string", minLength: 1 },
          title: { type: "string", minLength: 1, maxLength: 80 },
          timeout_ms: { type: "integer", minimum: 1, maximum: MAX_TIMEOUT_MS },
        },
        required: ["code"],
      },
    },
    {
      name: "js_reset",
      description: "Reset the persistent Computer Use JavaScript session and discard all bindings.",
      annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
      inputSchema: { type: "object", additionalProperties: false, properties: {} },
    },
  ];
}

function normalizeTimeout(value) {
  if (!Number.isInteger(value) || value <= 0) return DEFAULT_TIMEOUT_MS;
  return Math.min(value, MAX_TIMEOUT_MS);
}

class NativePeer {
  constructor(command) {
    this.command = command;
    this.nextID = 1;
    this.pending = new Map();
    this.toolNames = new Set();
    this.closing = false;
    const native = resolveNativeCommand(command);
    this.child = spawn(native.command, native.args, {
      env: { ...process.env, OPEN_COMPUTER_USE_ACTION_READ_BACK: "0" },
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    this.child.stderr.on("data", (chunk) => process.stderr.write(chunk));
    const lines = readline.createInterface({ input: this.child.stdout, crlfDelay: Infinity });
    lines.on("line", (line) => this.#handleLine(line));
    this.child.on("error", (error) => this.#failFatal(error));
    this.child.on("exit", (code, signal) => this.#failFatal(new Error(`open-computer-use exited (code=${code} signal=${signal ?? ""})`)));
  }

  async initialize() {
    await this.request("initialize", {
      protocolVersion: "2025-06-18",
      capabilities: {},
      clientInfo: { name: "mcpx-computer-use-repl", version: "1" },
    });
    this.notify("notifications/initialized", {});
    try {
      const listed = await this.request("tools/list", {});
      this.toolNames = new Set(
        (Array.isArray(listed?.tools) ? listed.tools : [])
          .map((tool) => tool?.name)
          .filter((name) => typeof name === "string" && name !== ""),
      );
    } catch {
      // Capability discovery is best-effort. Adapters must keep working with
      // native servers that do not implement tools/list reliably.
      this.toolNames = new Set();
    }
  }

  hasTool(name) {
    return this.toolNames.has(name);
  }

  request(method, params, timeoutMs = NATIVE_REQUEST_TIMEOUT_MS) {
    const id = this.nextID++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`open-computer-use ${method} timed out`));
      }, timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      try {
        this.child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
      } catch (error) {
        clearTimeout(timer);
        this.pending.delete(id);
        reject(error);
      }
    });
  }

  notify(method, params) {
    this.child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method, params })}\n`);
  }

  close() {
    this.closing = true;
    try {
      this.child.stdin.end();
    } catch {}
  }

  #failFatal(error) {
    this.#fail(error);
    if (this.closing) return;
    // A dead native peer invalidates this adapter session; exit so MCPX drops
    // the session and starts a fresh adapter/native pair on the next call.
    process.stderr.write(`mcpx-computer-use adapter: ${error.message}\n`);
    process.exit(1);
  }

  #handleLine(line) {
    let message;
    try {
      message = JSON.parse(line);
    } catch {
      return;
    }
    if (typeof message.id !== "number") return;
    const pending = this.pending.get(message.id);
    if (!pending) return;
    this.pending.delete(message.id);
    clearTimeout(pending.timer);
    if (message.error) {
      pending.reject(new Error(message.error.message ?? "open-computer-use request failed"));
      return;
    }
    pending.resolve(message.result);
  }

  #fail(error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
  }
}

function resolveNativeCommand(binaryPath) {
  if (process.platform === "win32" && /\.(cmd|bat)$/i.test(binaryPath)) {
    const quoted = `"${binaryPath.replaceAll('"', '""')}" mcp`;
    return { command: process.env.ComSpec || "cmd.exe", args: ["/d", "/s", "/c", quoted] };
  }
  return { command: binaryPath, args: ["mcp"] };
}

class WorkerJavaScriptSession {
  constructor(native, tools = new Set()) {
    this.native = native;
    this.tools = [...tools];
    this.nextID = 1;
    this.pending = new Map();
    this.queue = Promise.resolve();
    this.#start();
  }

  #start() {
    const worker = new Worker(new URL(import.meta.url), { workerData: { mode: "kernel", tools: this.tools } });
    this.worker = worker;
    worker.on("message", (message) => void this.#handleMessage(worker, message));
    worker.on("error", (error) => this.#failWorker(worker, error));
    worker.on("exit", (code) => {
      if (code !== 0) this.#failWorker(worker, new Error(`JavaScript kernel exited with code ${code}`));
    });
  }

  async #handleMessage(worker, message) {
    if (message.type === "result") {
      const pending = this.pending.get(message.id);
      if (!pending || pending.worker !== worker) return;
      this.pending.delete(message.id);
      clearTimeout(pending.timer);
      pending.resolve(message.result);
      return;
    }
    if (message.type === "native_request") {
      try {
        const result = await this.native.request(message.method, message.params, message.timeoutMs);
        if (this.worker === worker) worker.postMessage({ type: "native_response", id: message.id, result });
      } catch (error) {
        if (this.worker === worker) worker.postMessage({ type: "native_response", id: message.id, error: error instanceof Error ? error.message : String(error) });
      }
    }
  }

  #failWorker(worker, error) {
    for (const [id, pending] of this.pending) {
      if (pending.worker !== worker) continue;
      clearTimeout(pending.timer);
      pending.resolve(errorResult(error));
      this.pending.delete(id);
    }
  }

  async reset() {
    const old = this.worker;
    this.#failWorker(old, new Error("JavaScript session reset"));
    await old.terminate().catch(() => {});
    if (this.worker === old) this.#start();
  }

  async close() {
    const old = this.worker;
    this.#failWorker(old, new Error("JavaScript session closed"));
    await old?.terminate().catch(() => {});
    this.worker = null;
  }

  run(code, timeoutMs, meta) {
    const queued = this.queue.then(() => this.#run(code, timeoutMs, meta));
    this.queue = queued.catch(() => {});
    return queued;
  }

  #run(code, timeoutMs, meta) {
    if (typeof code !== "string" || code.trim() === "") {
      return Promise.resolve(errorResult(new Error("js expects non-empty JavaScript source")));
    }
    const id = this.nextID++;
    return new Promise((resolve) => {
      const worker = this.worker;
      const timer = setTimeout(async () => {
        const pending = this.pending.get(id);
        if (!pending || pending.worker !== worker) return;
        this.pending.delete(id);
        await worker.terminate().catch(() => {});
        if (this.worker === worker) this.#start();
        resolve(errorResult(new Error(`js execution timed out after ${timeoutMs} ms; session reset`)));
      }, timeoutMs);
      this.pending.set(id, { resolve, timer, worker });
      worker.postMessage({ type: "exec", id, code, requestMeta: meta });
    });
  }
}

function errorResult(error) {
  return { content: [{ type: "text", text: `Error: ${error instanceof Error ? error.message : String(error)}` }], isError: true };
}

function runKernel() {
  const toolNames = new Set(Array.isArray(workerData?.tools) ? workerData.tools : []);
  const native = {
    nextID: 1,
    pending: new Map(),
    hasTool(name) {
      return toolNames.has(name);
    },
    request(method, params, timeoutMs) {
      const id = this.nextID++;
      return new Promise((resolve, reject) => {
        this.pending.set(id, { resolve, reject });
        parentPort.postMessage({ type: "native_request", id, method, params, timeoutMs });
      });
    },
  };
  const session = new PersistentJavaScriptSession(native);
  parentPort.on("message", async (message) => {
    if (message.type === "native_response") {
      const pending = native.pending.get(message.id);
      if (!pending) return;
      native.pending.delete(message.id);
      if (message.error) pending.reject(new Error(message.error));
      else pending.resolve(message.result);
      return;
    }
    if (message.type === "exec") {
      const result = await session.run(message.code, message.requestMeta ?? {});
      parentPort.postMessage({ type: "result", id: message.id, result });
    }
  });
}

class PersistentJavaScriptSession {
  constructor(native) {
    this.native = native;
    this.activeOutput = null;
    this.reset();
  }

  reset() {
    this.repl?.close();
    const input = new PassThrough();
    const output = new PassThrough();
    output.resume();
    const settle = (error) => {
      if (this.activeOutput?.reject) this.activeOutput.reject(error);
      return "ignore";
    };
    this.repl = replModule.start({
      prompt: "",
      input,
      output,
      terminal: false,
      useGlobal: false,
      ignoreUndefined: true,
      breakEvalOnSigint: true,
      handleError: settle,
    });
    this.repl.on("error", () => {});
    this.repl._domain?.on("error", settle);

    const nodeRepl = {
      cwd: process.cwd(),
      homeDir: process.env.HOME ?? "",
      tmpDir: process.env.TMPDIR ?? "/tmp",
      write: (value) => this.#output().write(value),
      emitImage: async (value) => this.#output().emitImage(value),
    };
    Object.defineProperty(nodeRepl, "requestMeta", { get: () => this.requestMeta ?? {} });
    Object.freeze(nodeRepl);
    Object.defineProperty(this.repl.context, "nodeRepl", { value: nodeRepl, configurable: false, writable: false });
    Object.defineProperty(this.repl.context, "cua", { value: createCuaAPI(this.native, () => this.#output()), configurable: false, writable: false });
  }

  #output() {
    if (!this.activeOutput) throw new Error("nodeRepl output is only available while js is executing");
    return this.activeOutput;
  }

  async run(code, requestMeta = {}) {
    this.requestMeta = requestMeta;
    const content = [];
    const active = {
      write(value) {
        content.push({ type: "text", text: formatValue(value) });
      },
      async emitImage(value) {
        const image = normalizeImage(await value);
        content.push({ type: "image", mimeType: image.mimeType, data: image.data });
      },
      reject: null,
    };
    this.activeOutput = active;
    try {
      await new Promise((resolve, reject) => {
        active.reject = reject;
        this.repl.eval(code, this.repl.context, "mcpx-computer-use", (error, result) => {
          if (error) reject(error);
          else resolve(result);
        });
      });
      if (content.length === 0) content.push({ type: "text", text: "(no output)" });
      return { content, isError: false };
    } catch (error) {
      content.push({ type: "text", text: `Error: ${error instanceof Error ? error.message : String(error)}` });
      return { content, isError: true };
    } finally {
      this.activeOutput = null;
    }
  }
}

function createCuaAPI(native, activeOutput) {
  let docsEmitted = false;
  async function emitDocs() {
    if (docsEmitted) return;
    activeOutput().write(CUA_GUIDANCE);
    docsEmitted = true;
  }
  async function call(tool, args = {}) {
    const result = await native.request("tools/call", { name: tool, arguments: args });
    if (result?.isError) throw new Error(toolResultText(result) || `${tool} failed`);
    return result;
  }
  async function emitText(text, options = {}) {
    if (options.emit === false || !text) return;
    activeOutput().write(text);
  }
  async function emitImages(images, options = {}) {
    if (options.emit === false) return;
    for (const image of images) {
      await activeOutput().emitImage({ bytes: Buffer.from(image.data ?? "", "base64"), mimeType: image.mimeType ?? "image/png" });
    }
  }

  function appBinding(app) {
    async function pressKey(key) {
      const normalized = normalizeKeySpec(key);
      await call("press_key", { app, key: normalized });
      return { key: normalized };
    }

    async function typeText(text, options) {
      if (typeof text !== "string") throw new Error("typeText text must be a string");
      const normalizedOptions = normalizeTextOptions(options);
      if (normalizedOptions.focus !== undefined) {
        const focus = normalizedOptions.focus;
        if (Array.isArray(focus)) {
          const point = parseDrawPoint(focus, "typeText.focus");
          await call("click", { app, x: point[0], y: point[1] });
        } else if (typeof focus === "string" || typeof focus === "number") {
          await call("click", { app, element_index: String(focus) });
        } else {
          throw new Error("typeText.focus must be an element index or [x, y]");
        }
      }
      await call("type_text", { app, text });
      if (normalizedOptions.submit) await pressKey("Return");
      return { characters: [...text].length, submitted: normalizedOptions.submit };
    }

    async function hotkey(...keys) {
      const normalized = normalizeHotkey(keys);
      await pressKey(normalized);
      return { key: normalized };
    }

    async function keySequence(sequence, options) {
      if (!Array.isArray(sequence) || sequence.length === 0) {
        throw new Error("keySequence must contain at least one step");
      }
      if (sequence.length > MAX_KEY_SEQUENCE_STEPS) {
        throw new Error(`keySequence supports at most ${MAX_KEY_SEQUENCE_STEPS} steps`);
      }
      const normalizedOptions = normalizeTextOptions(options);
      const delayMs = normalizeDelayMs(normalizedOptions.delayMs);
      for (let index = 0; index < sequence.length; index++) {
        const step = sequence[index];
        if (typeof step === "string") {
          await pressKey(step);
        } else if (step && typeof step === "object" && !Array.isArray(step)) {
          const fields = ["key", "text", "sleepMs"].filter((field) => step[field] !== undefined);
          if (fields.length !== 1) {
            throw new Error(`keySequence[${index}] must set exactly one of key, text, or sleepMs`);
          }
          if (step.key !== undefined) await pressKey(step.key);
          else if (step.text !== undefined) await typeText(String(step.text));
          else await sleep(normalizeDelayMs(step.sleepMs));
        } else {
          throw new Error(`keySequence[${index}] must be a string or an object`);
        }
        if (delayMs > 0 && index < sequence.length - 1) await sleep(delayMs);
      }
      return { steps: sequence.length };
    }

    async function dragPath(options) {
      if (!options || typeof options !== "object" || Array.isArray(options)) {
        throw new Error("dragPath options must be an object");
      }
      const defaultSegments = native.hasTool("drag_path") ? DEFAULT_NATIVE_DRAG_SEGMENTS : DEFAULT_FALLBACK_DRAG_SEGMENTS;
      const points = await resolveDragPathPoints(options, defaultSegments);
      const durationMs = normalizeDragDuration(options.durationMs);
      const mouseButton = normalizeMouseButton(options.mouseButton);
      const delivery = normalizeDelivery(options.delivery);

      if (native.hasTool("drag_path")) {
        await call("drag_path", {
          app,
          points: points.map(([x, y]) => ({ x, y })),
          duration_ms: durationMs,
          mouse_button: mouseButton,
          delivery,
        });
        return {
          mode: "drag_path",
          segments: points.length - 1,
          points,
          durationMs,
          mouseButton,
          delivery,
        };
      }

      if (mouseButton !== "left") {
        throw new Error("dragPath fallback only supports the left mouse button; native drag_path is required");
      }
      if (points.length - 1 > MAX_FALLBACK_DRAG_SEGMENTS) {
        throw new Error(`dragPath with more than ${MAX_FALLBACK_DRAG_SEGMENTS} segments requires native drag_path`);
      }
      const delayMs = normalizeDelayMs(options.delayMs);
      for (let index = 1; index < points.length; index++) {
        const from = points[index - 1];
        const to = points[index];
        await call("drag", { app, from_x: from[0], from_y: from[1], to_x: to[0], to_y: to[1] });
        if (delayMs > 0 && index < points.length - 1) await sleep(delayMs);
      }
      return {
        mode: "multi_drag",
        segments: points.length - 1,
        points,
        durationMs: null,
        mouseButton,
        delivery: "app_post",
      };
    }

    return Object.freeze({
      async getAXState(options = {}) {
        const result = await call("get_app_state", { app, ...snapshotArgs(options) });
        const text = toolResultText(result);
        await emitText(text, options);
        return text;
      },
      async getScreenshot(options = {}) {
        const result = await call("get_app_state", { app, text_limit: 1 });
        const image = toolResultImages(result)[0];
        if (!image) throw new Error(`screenshot unavailable for ${app}`);
        await emitImages([image], options);
        return Buffer.from(image.data, "base64");
      },
      async getAXStateAndScreenshot(options = {}) {
        const result = await call("get_app_state", { app, ...snapshotArgs(options) });
        const state = toolResultText(result);
        const images = toolResultImages(result);
        await emitText(state, options);
        await emitImages(images, options);
        return images[0] ? { state, screenshot: Buffer.from(images[0].data, "base64") } : { state };
      },
      async click(target, options = {}) {
        const targetArgs = Array.isArray(target) ? { x: target[0], y: target[1] } : { element_index: target };
        await call("click", { app, ...targetArgs, ...clickArgs(options) });
      },
      async drag(from, to) {
        const start = parseDrawPoint(from, "drag.from");
        const end = parseDrawPoint(to, "drag.to");
        await call("drag", { app, from_x: start[0], from_y: start[1], to_x: end[0], to_y: end[1] });
      },
      dragPath,
      hotkey,
      keySequence,
      pressKey,
      typeText,
      async scroll(target, direction, pages = 1) {
        if (Array.isArray(target)) throw new Error("coordinate scroll is not supported");
        await call("scroll", { app, element_index: target, direction, pages });
      },
      async setValue(elementIndex, value) {
        await call("set_value", { app, element_index: elementIndex, value });
      },
      async performSecondaryAction(elementIndex, action) {
        await call("perform_secondary_action", { app, element_index: elementIndex, action });
      },
    });
  }

  return Object.freeze({
    async getState(options = {}) {
      await emitDocs();
      const result = await call("list_apps", {});
      const text = toolResultText(result);
      let apps = text;
      try {
        const parsed = JSON.parse(text);
        apps = Array.isArray(parsed) ? parsed : parseListApps(text);
      } catch {
        apps = parseListApps(text);
      }
      if (options.emit !== false) activeOutput().write(apps);
      return { apps };
    },
    async listApps(options = {}) {
      const state = await this.getState({ emit: false });
      if (options.emit !== false) activeOutput().write(state.apps);
      return state.apps;
    },
    async getApp(app) {
      await emitDocs();
      const result = await call("get_app_state", { app, text_limit: "max" });
      await emitText(toolResultText(result));
      return appBinding(app);
    },
  });
}

async function resolveDragPathPoints(options, defaultSegments) {
  if (options.points !== undefined) {
    if (options.from !== undefined || options.to !== undefined || options.curve !== undefined) {
      throw new Error("dragPath accepts either points or from/to with curve, not both");
    }
    return normalizePathPoints(options.points);
  }
  const from = parseDrawPoint(options.from, "dragPath.from");
  const to = parseDrawPoint(options.to, "dragPath.to");
  const segments = normalizeDragSegments(options.steps, defaultSegments);
  if (typeof options.curve === "function") {
    return sampleCurveFunction(options.curve, from, to, segments);
  }
  const curve = options.curve === undefined || options.curve === null ? "line" : String(options.curve).toLowerCase();
  switch (curve) {
    case "line":
      return sampleLine(from, to, segments);
    case "quadratic": {
      const control = parseDrawPoint(options.control, "dragPath.control");
      return sampleQuadraticBezier(from, control, to, segments);
    }
    case "cubic": {
      const control1 = parseDrawPoint(options.control1, "dragPath.control1");
      const control2 = parseDrawPoint(options.control2, "dragPath.control2");
      return sampleCubicBezier(from, control1, control2, to, segments);
    }
    default:
      throw new Error(`unsupported dragPath curve ${JSON.stringify(options.curve)}`);
  }
}

async function sampleCurveFunction(curve, from, to, segments) {
  const points = [];
  for (let index = 0; index <= segments; index++) {
    const progress = index / segments;
    const value = await curve(progress, [...from], [...to]);
    points.push(parseDrawPoint(value, `dragPath.curve(${progress})`));
  }
  points[0] = [...from];
  points[points.length - 1] = [...to];
  return points;
}

function sampleLine(from, to, segments) {
  const points = [];
  for (let index = 0; index <= segments; index++) {
    const progress = index / segments;
    points.push([
      from[0] + (to[0] - from[0]) * progress,
      from[1] + (to[1] - from[1]) * progress,
    ]);
  }
  return points;
}

function sampleQuadraticBezier(from, control, to, segments) {
  const points = [];
  for (let index = 0; index <= segments; index++) {
    const progress = index / segments;
    const inverse = 1 - progress;
    points.push([
      inverse ** 2 * from[0] + 2 * inverse * progress * control[0] + progress ** 2 * to[0],
      inverse ** 2 * from[1] + 2 * inverse * progress * control[1] + progress ** 2 * to[1],
    ]);
  }
  return points;
}

function sampleCubicBezier(from, control1, control2, to, segments) {
  const points = [];
  for (let index = 0; index <= segments; index++) {
    points.push(cubicBezierPoint(from, control1, control2, to, index / segments));
  }
  return points;
}

function cubicBezierPoint(from, control1, control2, to, progress) {
  const inverse = 1 - progress;
  const fromWeight = inverse ** 3;
  const control1Weight = 3 * inverse ** 2 * progress;
  const control2Weight = 3 * inverse * progress ** 2;
  const toWeight = progress ** 3;
  return [
    from[0] * fromWeight + control1[0] * control1Weight + control2[0] * control2Weight + to[0] * toWeight,
    from[1] * fromWeight + control1[1] * control1Weight + control2[1] * control2Weight + to[1] * toWeight,
  ];
}

function normalizePathPoints(points) {
  if (!Array.isArray(points) || points.length < 2) {
    throw new Error("dragPath points must contain at least two points");
  }
  if (points.length - 1 > MAX_DRAG_SEGMENTS) {
    throw new Error(`dragPath supports at most ${MAX_DRAG_SEGMENTS} segments`);
  }
  return points.map((point, index) => parseDrawPoint(point, `dragPath.points[${index}]`));
}

function parseDrawPoint(value, label) {
  let point;
  if (Array.isArray(value) && value.length === 2) {
    point = [value[0], value[1]];
  } else if (value && typeof value === "object" && "x" in value && "y" in value) {
    point = [value.x, value.y];
  } else {
    throw new Error(`${label} must be [x, y] or { x, y }`);
  }
  for (const [index, coordinate] of point.entries()) {
    if (typeof coordinate !== "number" || !Number.isFinite(coordinate)) {
      throw new Error(`${label}[${index}] must be a finite number`);
    }
  }
  return point;
}

function normalizeDragSegments(value, defaultValue) {
  if (value === undefined) return defaultValue;
  if (!Number.isInteger(value) || value < 2 || value > MAX_DRAG_SEGMENTS) {
    throw new Error(`dragPath steps must be an integer between 2 and ${MAX_DRAG_SEGMENTS}`);
  }
  return value;
}

function normalizeDragDuration(value) {
  if (value === undefined) return DEFAULT_DRAG_DURATION_MS;
  if (typeof value !== "number" || !Number.isFinite(value) || value < MIN_DRAG_DURATION_MS || value > MAX_DRAG_DURATION_MS) {
    throw new Error(`dragPath durationMs must be between ${MIN_DRAG_DURATION_MS} and ${MAX_DRAG_DURATION_MS}`);
  }
  return value;
}

function normalizeMouseButton(value) {
  const normalized = value === undefined ? "left" : String(value).toLowerCase();
  if (!["left", "right", "middle"].includes(normalized)) {
    throw new Error("dragPath mouseButton must be left, right, or middle");
  }
  return normalized;
}

function normalizeDelivery(value) {
  const normalized = value === undefined ? "app_post" : String(value).toLowerCase();
  if (!["app_post", "global"].includes(normalized)) {
    throw new Error("dragPath delivery must be app_post or global");
  }
  return normalized;
}

function normalizeDelayMs(value) {
  if (value === undefined) return 0;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > MAX_DELAY_MS) {
    throw new Error(`delayMs must be between 0 and ${MAX_DELAY_MS}`);
  }
  return value;
}

function normalizeTextOptions(options) {
  if (options === undefined) return { submit: false };
  if (!options || typeof options !== "object" || Array.isArray(options)) {
    throw new Error("text options must be an object");
  }
  return {
    submit: options.submit === true,
    focus: options.focus,
    delayMs: options.delayMs,
  };
}

function normalizeKeySpec(value) {
  if (typeof value !== "string") throw new Error("key must be a string");
  const normalized = value.split("+").map((part) => part.trim()).filter((part) => part !== "").join("+");
  if (normalized === "") throw new Error("key must not be empty");
  return normalized;
}

function normalizeHotkey(keys) {
  const parts = [];
  const visit = (value) => {
    if (Array.isArray(value)) {
      for (const child of value) visit(child);
      return;
    }
    if (typeof value !== "string" || value.trim() === "") {
      throw new Error("hotkey keys must be non-empty strings");
    }
    parts.push(value.trim());
  };
  for (const value of keys) visit(value);
  if (parts.length === 0) throw new Error("hotkey requires at least one key");
  return parts.join("+");
}

function sleep(durationMs) {
  return new Promise((resolve) => setTimeout(resolve, durationMs));
}

function parseListApps(text) {
  if (!text.trim() || /^No running top-level apps are visible/u.test(text.trim())) return [];
  return text.split(/\r?\n/).map((line) => {
    const trimmed = line.trim();
    const match = /^(.*?)\s+(?:—|--)\s+(.+?)\s+\[([^\]]*)\]$/u.exec(trimmed);
    if (!match) return { id: trimmed, displayName: trimmed };
    return { id: match[2], displayName: match[1], ...parseListAppMarkers(match[3]) };
  }).filter((app) => app.id);
}

function parseListAppMarkers(markers) {
  const app = {};
  for (const marker of markers.split(/,\s*/)) {
    if (marker === "running") app.isRunning = true;
    else if (marker.startsWith("last-used=")) app.lastUsedDate = marker.slice("last-used=".length);
    else if (marker.startsWith("uses=")) {
      const useCount = Number.parseInt(marker.slice("uses=".length), 10);
      if (Number.isSafeInteger(useCount)) app.useCount = useCount;
    }
  }
  return app;
}

function snapshotArgs(options = {}) {
  const args = {};
  if (options.textLimit !== undefined) args.text_limit = options.textLimit;
  if (options.maxTreeNodes !== undefined) args.max_tree_nodes = options.maxTreeNodes;
  if (options.maxTreeDepth !== undefined) args.max_tree_depth = options.maxTreeDepth;
  return args;
}

function clickArgs(options = {}) {
  const args = {};
  if (options.mouseButton !== undefined) args.mouse_button = options.mouseButton;
  if (options.clickCount !== undefined) args.click_count = options.clickCount;
  if (options.clickMethod !== undefined) args.click_method = options.clickMethod;
  return args;
}

function toolResultText(result) {
  return (Array.isArray(result?.content) ? result.content : [])
    .filter((item) => item?.type === "text" && typeof item.text === "string")
    .map((item) => item.text)
    .join("\n");
}

function toolResultImages(result) {
  return (Array.isArray(result?.content) ? result.content : []).filter((item) => item?.type === "image" && typeof item.data === "string");
}

function formatValue(value) {
  if (typeof value === "string") return value;
  return util.inspect(value, { depth: 6, colors: false, breakLength: 100 });
}

function normalizeImage(value) {
  if (value && typeof value === "object" && value.bytes instanceof Uint8Array) {
    return { data: Buffer.from(value.bytes).toString("base64"), mimeType: value.mimeType ?? "image/png" };
  }
  if (value && typeof value === "object" && typeof value.data_url === "string") {
    return dataURLImage(value.data_url);
  }
  if (typeof value === "string" && value.startsWith("data:image/")) {
    return dataURLImage(value);
  }
  throw new Error("nodeRepl.emitImage expects bytes with mimeType or an image data URL");
}

function dataURLImage(value) {
  const match = /^data:(image\/[^;]+);base64,(.*)$/s.exec(value);
  if (!match) throw new Error("nodeRepl.emitImage received an invalid image data URL");
  return { data: match[2], mimeType: match[1] };
}

if (isMainThread) {
  await runServer(process.argv[2]);
} else if (workerData?.mode === "kernel") {
  runKernel();
} else {
  throw new Error("unsupported worker mode");
}
