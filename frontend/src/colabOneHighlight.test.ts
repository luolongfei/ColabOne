import { describe, expect, it } from "vitest";
import {
  highlightColabOneCode,
  normalizeColabOneCodeLanguage,
  type ColabOneHighlightedLine,
} from "./colabOneHighlight";

function reconstruct(lines: ColabOneHighlightedLine[]): string {
  return lines
    .map((line) => line.map((token) => token.content).join(""))
    .join("\n");
}

describe("ColabOne syntax highlighting engine", () => {
  it("normalizes language aliases cleanly", () => {
    expect(normalizeColabOneCodeLanguage("curl")).toBe("shellscript");
    expect(normalizeColabOneCodeLanguage("bash")).toBe("shellscript");
    expect(normalizeColabOneCodeLanguage("sh")).toBe("shellscript");
    expect(normalizeColabOneCodeLanguage("Python")).toBe("python");
    expect(normalizeColabOneCodeLanguage("py")).toBe("python");
    expect(normalizeColabOneCodeLanguage("codex")).toBe("toml");
    expect(normalizeColabOneCodeLanguage("toml")).toBe("toml");
    expect(normalizeColabOneCodeLanguage("json")).toBe("json");
    expect(normalizeColabOneCodeLanguage("unknown-lang")).toBe("text");
    expect(normalizeColabOneCodeLanguage(undefined)).toBe("text");
  });

  it("highlights cURL commands, flags, URLs, env vars, and embedded JSON with distinct light/dark colors", () => {
    const curlSample = `curl -N "https://YOUR-API-HOST/v1/chat/completions" \\
  -H "Authorization: Bearer $COLABONE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
  "model": "YOUR-MODEL-ID",
  "messages": [
    {
      "role": "user",
      "content": "用一句话介绍 ColabOne"
    }
  ],
  "max_tokens": 512,
  "stream": true
}'`;

    const lines = highlightColabOneCode(curlSample, "curl");
    expect(reconstruct(lines)).toBe(curlSample);

    const flat = lines.flat();
    const findToken = (text: string) =>
      flat.find((token) => token.content.trim() === text);

    const cmdToken = findToken("curl");
    const flagToken = findToken("-H");
    const urlToken = flat.find((token) =>
      token.content.includes("https://YOUR-API-HOST"),
    );
    const envVarToken = findToken("$COLABONE_API_KEY");
    const jsonKeyToken = findToken('"model"');
    const jsonValToken = findToken('"YOUR-MODEL-ID"');
    const jsonNumToken = findToken("512");
    const jsonBoolToken = findToken("true");

    expect(cmdToken?.lightColor).toBeTruthy();
    expect(cmdToken?.darkColor).toBeTruthy();
    expect(flagToken?.lightColor).toBeTruthy();
    expect(urlToken?.lightColor).toBeTruthy();
    expect(envVarToken?.lightColor).toBeTruthy();
    expect(jsonKeyToken?.lightColor).toBeTruthy();
    expect(jsonValToken?.lightColor).toBeTruthy();
    expect(jsonNumToken?.lightColor).toBeTruthy();
    expect(jsonBoolToken?.lightColor).toBeTruthy();

    // Command, flag, string, and env variable each receive distinct colors
    const shellColors = new Set([
      cmdToken!.lightColor,
      flagToken!.lightColor,
      urlToken!.lightColor,
      envVarToken!.lightColor,
    ]);
    expect(shellColors.size).toBe(4);

    // Embedded JSON keys, string values, and numbers receive distinct colors
    const jsonColors = new Set([
      jsonKeyToken!.lightColor,
      jsonValToken!.lightColor,
      jsonNumToken!.lightColor,
    ]);
    expect(jsonColors.size).toBe(3);
  });

  it("highlights Python SDK and Codex TOML snippets with exact character fidelity", () => {
    const pySample = `import os
from openai import OpenAI

client = OpenAI(
    base_url="https://api.example.com/v1",
    api_key=os.environ["COLABONE_API_KEY"],
)
reply = client.chat.completions.create(
    model="qwen-2.5",
    max_tokens=512,
    stream=True,
)`;
    const pyLines = highlightColabOneCode(pySample, "python");
    expect(reconstruct(pyLines)).toBe(pySample);
    const pyColors = new Set(
      pyLines
        .flat()
        .map((token) => token.lightColor)
        .filter(Boolean),
    );
    expect(pyColors.size).toBeGreaterThanOrEqual(4);

    const tomlSample = `# ~/.codex/config.toml
model = "YOUR-MODEL-ID"
model_context_window = 8192

[model_providers.colabone]
name = "ColabOne"
wire_api = "responses"
supports_websockets = false`;
    const tomlLines = highlightColabOneCode(tomlSample, "codex");
    expect(reconstruct(tomlLines)).toBe(tomlSample);
    const tomlColors = new Set(
      tomlLines
        .flat()
        .map((token) => token.lightColor)
        .filter(Boolean),
    );
    expect(tomlColors.size).toBeGreaterThanOrEqual(4);
  });

  it("handles edge cases (empty strings, CRLF, malformed JSON quotes, HTML characters) safely", () => {
    expect(reconstruct(highlightColabOneCode("", "curl"))).toBe("");
    expect(
      reconstruct(highlightColabOneCode("echo 'hello'\r\necho 'world'", "bash")),
    ).toBe("echo 'hello'\necho 'world'");

    const malformedShell = `curl -d '{not valid json}' "https://example.com/?a=1&b=<script>"`;
    const lines = highlightColabOneCode(malformedShell, "bash");
    expect(reconstruct(lines)).toBe(malformedShell);

    const unclosedQuote = `echo '{ "a": 1 }`;
    expect(reconstruct(highlightColabOneCode(unclosedQuote, "bash"))).toBe(
      unclosedQuote,
    );

    const plain = `plain <tag> & "quotes"`;
    expect(reconstruct(highlightColabOneCode(plain, "text"))).toBe(plain);

    // Standalone CR line endings and ANSI-C $'...' quoting with escaped quotes
    expect(
      reconstruct(highlightColabOneCode("echo 1\recho 2", "bash")),
    ).toBe("echo 1\necho 2");
    const ansiC = `echo $'{"a": "it\\'s"}'`;
    expect(reconstruct(highlightColabOneCode(ansiC, "bash"))).toBe(ansiC);

    // Oversized blobs fall back safely to plain text without freezing
    const giant = `curl "${"x".repeat(70000)}"`;
    const giantLines = highlightColabOneCode(giant, "curl");
    expect(reconstruct(giantLines)).toBe(giant);
    expect(giantLines[0][0].lightColor).toBeUndefined();

    // Repeated calls hit the LRU cache and return consistent tokenized structure
    const first = highlightColabOneCode("curl -I https://example.com", "curl");
    const second = highlightColabOneCode("curl -I https://example.com", "curl");
    expect(second).toBe(first);
  });
});
