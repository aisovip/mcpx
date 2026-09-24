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
- await app.performSecondaryAction(elementIndex, action)`;

async function runServer(binaryPath) {
  if (typeof binaryPath !== "string" || binaryPath.trim() === "") {
    throw new Error("open-computer-use binary path is required");
  }
  const native = new NativePeer(binaryPath.trim());
  await native.initialize();
  const session = new WorkerJavaScriptSession(native);
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
  constructor(native) {
    this.native = native;
    this.nextID = 1;
    this.pending = new Map();
    this.queue = Promise.resolve();
    this.#start();
  }

  #start() {
    const worker = new Worker(new URL(import.meta.url), { workerData: { mode: "kernel" } });
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
  const native = {
    nextID: 1,
    pending: new Map(),
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
        await call("drag", { app, from_x: from[0], from_y: from[1], to_x: to[0], to_y: to[1] });
      },
      async pressKey(key) {
        await call("press_key", { app, key });
      },
      async scroll(target, direction, pages = 1) {
        if (Array.isArray(target)) throw new Error("coordinate scroll is not supported");
        await call("scroll", { app, element_index: target, direction, pages });
      },
      async setValue(elementIndex, value) {
        await call("set_value", { app, element_index: elementIndex, value });
      },
      async typeText(text) {
        await call("type_text", { app, text });
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
