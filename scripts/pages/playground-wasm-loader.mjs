async function sha256(bytes) {
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return [...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, '0')).join('');
}

async function verify(bytes, expected) {
  if (bytes.byteLength !== expected.size || await sha256(bytes) !== expected.sha256) {
    throw new Error('Playground engine checksum mismatch. Reload this page.');
  }
}

async function download(url, expected, progress) {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`Playground engine: HTTP ${response.status}. Reload this page.`);
  const bytes = new Uint8Array(expected.size);
  const reader = response.body.getReader();
  let offset = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (offset + value.length > bytes.length) throw new Error('Playground download exceeds its recorded size.');
      bytes.set(value, offset);
      offset += value.length;
      progress({ type: 'progress', loaded: offset, total: bytes.length });
    }
  } finally {
    await reader.cancel();
  }
  if (offset !== bytes.length) throw new Error('Playground download is incomplete. Reload this page.');
  await verify(bytes, expected);
  return bytes;
}

async function decodeWithWasm(bytes, size) {
  const brotli = await import('./brotli-dec-wasm-2.3.2/brotli_dec_wasm.js');
  await brotli.default();
  const decoder = new brotli.DecompressStream();
  const decoded = new Uint8Array(size);
  let input = 0;
  let output = 0;
  try {
    for (;;) {
      const result = decoder.decompress(bytes.subarray(input, input + 65536), 1048576);
      let code, consumed, chunk;
      try {
        code = result.code;
        consumed = result.input_offset;
        chunk = result.buf;
      } finally {
        result.free();
      }
      input += consumed;
      if (output + chunk.length > decoded.length) throw new Error('Playground engine exceeds its recorded size.');
      decoded.set(chunk, output);
      output += chunk.length;
      if (code === brotli.BrotliStreamResultCode.ResultSuccess) {
        if (input !== bytes.length || output !== size) throw new Error('Playground engine size mismatch.');
        return decoded;
      }
      if ((!consumed && !chunk.length) ||
          (code === brotli.BrotliStreamResultCode.NeedsMoreInput && input === bytes.length)) {
        throw new Error('Playground engine is incomplete. Reload this page.');
      }
    }
  } finally {
    decoder.free();
  }
}

async function decompress(bytes, size) {
  let decoder;
  try {
    decoder = new DecompressionStream('brotli');
  } catch {
    return decodeWithWasm(bytes, size);
  }
  return new Uint8Array(await new Response(new Blob([bytes]).stream().pipeThrough(decoder)).arrayBuffer());
}

export async function fetchBrotliWasm(url, expected, progress) {
  const compressed = await download(url, expected.compressed, progress);
  const bytes = await decompress(compressed, expected.decoded.size);
  await verify(bytes, expected.decoded);
  return bytes.buffer;
}
