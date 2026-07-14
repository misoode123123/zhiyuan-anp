"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type ProjectSpace = { id: string; name: string; slug: string };
type Conversation = { id: string; status: string; title: string | null; requirement_id: string | null; created_at: string };
type Msg = { id: string; role: string; content: string; media_kind: string };
type Spec = { title: string; user_story: string; acceptance_criteria: string[] };

function parseText(c: string): string {
	try { return JSON.parse(c).text ?? c; } catch { return c; }
}

// encodeWAV 把 PCM Float32 编码为 16bit 单声道 WAV Blob。
function encodeWAV(samples: Float32Array, sampleRate: number): Blob {
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  const writeStr = (off: number, s: string) => { for (let i = 0; i < s.length; i++) view.setUint8(off + i, s.charCodeAt(i)); };
  writeStr(0, "RIFF"); view.setUint32(4, 36 + samples.length * 2, true); writeStr(8, "WAVE");
  writeStr(12, "fmt "); view.setUint32(16, 16, true); view.setUint16(20, 1, true); view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true); view.setUint32(28, sampleRate * 2, true); view.setUint16(32, 2, true); view.setUint16(34, 16, true);
  writeStr(36, "data"); view.setUint32(40, samples.length * 2, true);
  let off = 44;
  for (let i = 0; i < samples.length; i++, off += 2) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(off, s < 0 ? s * 0x8000 : s * 0x7fff, true);
  }
  return new Blob([buffer], { type: "audio/wav" });
}

export default function ChatPage() {
  const [spaces, setSpaces] = useState<ProjectSpace[]>([]);
  const [psID, setPsID] = useState("");
  const [convs, setConvs] = useState<Conversation[]>([]);
  const [cid, setCid] = useState("");
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [text, setText] = useState("");
  const [images, setImages] = useState<string[]>([]);
  const [sending, setSending] = useState(false);
  const [spec, setSpec] = useState<Spec | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [recording, setRecording] = useState(false);
  const mediaRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`).then((r) => r.json()).then((r: Envelope<ProjectSpace[]>) => {
      setSpaces(r.data ?? []);
      if (r.data?.[0]) setPsID(r.data[0].id);
    });
  }, []);
  useEffect(() => {
    if (!psID) return;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/conversations`).then((r) => r.json()).then((r: Envelope<Conversation[]>) => setConvs(r.data ?? []));
  }, [psID]);

  async function newConv() {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/conversations`, { method: "POST" });
    const r = await res.json();
    if (r.data?.id) {
      setCid(r.data.id); setMsgs([]); setSpec(null); setMsg("");
      setConvs((c) => [r.data, ...c]);
    }
  }
  async function openConv(id: string) {
    setCid(id); setSpec(null); setMsg("");
    const res = await fetch(`${API_BASE_URL}/conversations/${id}`);
    const r = await res.json();
    setMsgs(r.data?.messages ?? []);
  }
  async function send() {
    if (!text.trim() || !cid || sending) return;
    setSending(true);
    const cur = text;
    setMsgs((m) => [...m, { id: "tmp_u", role: "user", content: JSON.stringify({ text: cur }), media_kind: images.length ? "image" : "text" }]);
    const aspId = "tmp_a";
    setMsgs((m) => [...m, { id: aspId, role: "assistant", content: JSON.stringify({ text: "" }), media_kind: "text" }]);
    setText(""); const sent = images; setImages([]);
    try {
      const res = await fetch(`${API_BASE_URL}/conversations/${cid}/messages`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: cur, images: sent }),
      });
      if (!res.ok || !res.body) throw new Error("流式响应失败");
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      let aspText = "";
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split("\n");
        buf = lines.pop() ?? "";
        for (const line of lines) {
          if (!line.startsWith("data: ")) continue;
          const payload = line.slice(6);
          try {
            const d = JSON.parse(payload);
            if (d.delta) {
              aspText += d.delta;
              setMsgs((m) => m.map((x) => (x.id === aspId ? { ...x, content: JSON.stringify({ text: aspText }) } : x)));
            }
            if (d.done && d.message) setMsgs((m) => m.map((x) => (x.id === aspId ? d.message : x)));
            if (d.error) setMsg(`✗ ${d.error}`);
          } catch { /* ignore */ }
        }
      }
    } catch (e) { setMsg(`✗ ${e}`); }
    setSending(false);
  }
  function onFiles(files: FileList | null) {
    if (!files) return;
    Array.from(files).slice(0, 4).forEach((f) => {
      const rd = new FileReader();
      rd.onload = () => setImages((p) => [...p, rd.result as string]);
      rd.readAsDataURL(f);
    });
  }
  async function genSpec() {
    if (!cid || busy) return;
    setBusy(true); setMsg("");
    try {
      const res = await fetch(`${API_BASE_URL}/conversations/${cid}/generate-spec`, { method: "POST" });
      const r = await res.json();
      if (r.data) setSpec(r.data);
      else setMsg(`✗ ${r.message ?? "生成失败"}`);
    } catch (e) { setMsg(`✗ ${e}`); }
    setBusy(false);
  }
  async function commit() {
    if (!spec || !cid || busy) return;
    setBusy(true);
    try {
      const res = await fetch(`${API_BASE_URL}/conversations/${cid}/commit`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify(spec),
      });
      const r = await res.json();
      if (r.data?.requirement) {
        setMsg(`✓ 已生成需求 ${r.data.requirement.id}，可去需求工作台派发编码`);
        setSpec(null);
      } else setMsg(`✗ ${r.message ?? "提交失败"}`);
    } catch (e) { setMsg(`✗ ${e}`); }
    setBusy(false);
  }

  function exportMD() {
    if (!cid) return;
    const lines: string[] = ["# 需求对话记录", ""];
    for (const m of msgs) {
      const t = parseText(m.content);
      lines.push(m.role === "user" ? `**用户：** ${t}` : `**AI：** ${t}`, "");
    }
    if (spec) {
      lines.push("## 规格草稿", `- 标题：${spec.title}`, `- 用户故事：${spec.user_story}`, "- 验收标准：", ...spec.acceptance_criteria.map((a) => `  - ${a}`));
    }
    const blob = new Blob([lines.join("\n")], { type: "text/markdown" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `conversation-${cid.slice(0, 12)}.md`;
    a.click();
    URL.revokeObjectURL(url);
  }

  async function toggleRec() {
    if (recording) {
      mediaRef.current?.stop();
      return;
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mr = new MediaRecorder(stream);
      chunksRef.current = [];
      mr.ondataavailable = (e) => { if (e.data.size) chunksRef.current.push(e.data); };
      mr.onstop = async () => {
        setRecording(false);
        stream.getTracks().forEach((t) => t.stop());
        try {
          // webm → decodeAudioData → PCM → WAV（智谱 GLM-ASR 不支持 webm，需 wav）
          const webm = new Blob(chunksRef.current, { type: "audio/webm" });
          const arrayBuf = await webm.arrayBuffer();
          const audioCtx = new AudioContext();
          const audioBuf = await audioCtx.decodeAudioData(arrayBuf);
          const wav = encodeWAV(audioBuf.getChannelData(0), audioBuf.sampleRate);
          audioCtx.close();
          const b64 = await new Promise<string>((res) => {
            const r = new FileReader();
            r.onload = () => res((r.result as string).split(",")[1]);
            r.readAsDataURL(wav);
          });
          const res = await fetch(`${API_BASE_URL}/asr`, {
            method: "POST", headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ audio: b64, filename: "audio.wav" }),
          });
          const r = await res.json();
          if (r.data?.text) setText((t) => (t ? t + " " : "") + r.data.text);
          else setMsg(`✗ ASR: ${r.message ?? "失败"}`);
        } catch (e) { setMsg(`✗ ASR 处理: ${e}`); }
      };
      mediaRef.current = mr;
      mr.start();
      setRecording(true);
    } catch (e) { setMsg(`✗ 麦克风: ${e}`); }
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">💬 对话式需求梳理</h1>
      <p className="mb-4 text-sm text-neutral-600">
        AI agent 引导对话梳理需求，确认后生成规格入库 → <Link href="/requirements" className="text-blue-600">返回需求工作台</Link>
      </p>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[220px_1fr]">
        <div>
          <select value={psID} onChange={(e) => setPsID(e.target.value)} className="mb-2 w-full rounded border border-neutral-300 px-2 py-1 text-sm">
            {spaces.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          <button onClick={newConv} className="mb-3 w-full rounded-md bg-blue-600 px-3 py-1.5 text-sm text-white">＋ 新对话</button>
          <div className="space-y-1">
            {convs.map((c) => (
              <button key={c.id} onClick={() => openConv(c.id)} className={`block w-full truncate rounded px-2 py-1 text-left text-xs ${cid === c.id ? "bg-blue-50 text-blue-700" : "hover:bg-neutral-100"}`}>
                {c.status === "submitted" ? "✓ " : ""}{c.title ?? "新对话"}
              </button>
            ))}
          </div>
        </div>

        <div className="flex flex-col rounded-lg border border-neutral-200 bg-white" style={{ minHeight: "60vh" }}>
          {!cid ? (
            <div className="p-10 text-center text-sm text-neutral-400">点「＋ 新对话」开始梳理需求</div>
          ) : (
            <>
              <div className="space-y-3 overflow-auto p-4" style={{ maxHeight: "52vh" }}>
                {msgs.map((m, i) => {
                  const t = parseText(m.content);
                  const mine = m.role === "user";
                  return (
                    <div key={m.id + i} className={`flex ${mine ? "justify-end" : "justify-start"}`}>
                      <div className={`max-w-[80%] whitespace-pre-wrap rounded-lg px-3 py-2 text-sm ${mine ? "bg-blue-600 text-white" : "bg-neutral-100 text-neutral-800"}`}>{t}</div>
                    </div>
                  );
                })}
                {sending && <div className="text-xs text-neutral-400">AI 思考中…</div>}
              </div>
              <div className="mt-auto border-t border-neutral-200 p-3">
                {images.length > 0 && (
                  <div className="mb-2 flex gap-2">{images.map((img, i) => <img key={i} src={img} alt="" className="h-12 rounded border" />)}</div>
                )}
                <div className="flex flex-wrap gap-2">
                  <button onClick={toggleRec} className={`rounded px-2 py-1 text-sm text-white ${recording ? "animate-pulse bg-red-600" : "bg-neutral-700"}`} title="语音输入">{recording ? "⏹ 停止" : "🎤"}</button>
                  <input type="file" accept="image/*" multiple onChange={(e) => onFiles(e.target.files)} className="text-xs" />
                  <input
                    value={text} onChange={(e) => setText(e.target.value)}
                    onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); } }}
                    placeholder="描述你的需求…（Enter 发送）" className="min-w-0 flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
                  />
                  <button onClick={send} disabled={sending} className="rounded bg-blue-600 px-3 py-1 text-sm text-white disabled:opacity-50">发送</button>
                  <button onClick={genSpec} disabled={busy} className="rounded bg-emerald-600 px-3 py-1 text-sm text-white disabled:opacity-50">生成规格</button>
                  <button onClick={exportMD} disabled={msgs.length === 0} className="rounded bg-neutral-700 px-3 py-1 text-sm text-white disabled:opacity-50">⤓ 导出</button>
                </div>
              </div>
            </>
          )}
        </div>
      </div>

      {msg && <div className="mt-3 rounded-md bg-blue-50 p-2 text-sm text-blue-800">{msg}</div>}

      {spec && (
        <div className="mt-4 rounded-lg border border-emerald-200 bg-emerald-50 p-4">
          <div className="mb-2 text-sm font-semibold">📋 规格草稿（可编辑后确认入库）</div>
          <input value={spec.title} onChange={(e) => setSpec({ ...spec, title: e.target.value })} className="mb-2 w-full rounded border px-2 py-1 text-sm" />
          <textarea value={spec.user_story} onChange={(e) => setSpec({ ...spec, user_story: e.target.value })} rows={2} placeholder="用户故事" className="mb-2 w-full rounded border px-2 py-1 text-sm" />
          <textarea
            value={spec.acceptance_criteria.join("\n")} onChange={(e) => setSpec({ ...spec, acceptance_criteria: e.target.value.split("\n").filter(Boolean) })}
            rows={4} placeholder="验收标准（每行一条）" className="mb-2 w-full rounded border px-2 py-1 text-sm"
          />
          <button onClick={commit} disabled={busy} className="rounded bg-emerald-700 px-4 py-1.5 text-sm text-white">确认入库（生成需求）</button>
        </div>
      )}
    </div>
  );
}
