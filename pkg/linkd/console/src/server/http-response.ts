// 流式读取按原始字节限制 JSON 响应；成功、超限和解码失败均释放 reader。
export async function boundedJSON(
  response: Response,
  limit: number,
): Promise<unknown> {
  if (!response.body) throw new Error("empty response");
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let bytes = 0;
  try {
    for (;;) {
      const part = await reader.read();
      if (part.done) break;
      bytes += part.value.byteLength;
      if (bytes > limit) throw new Error("response limit");
      chunks.push(part.value);
    }
    return JSON.parse(Buffer.concat(chunks, bytes).toString("utf8")) as unknown;
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
}
