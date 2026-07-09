// Try-out script generator: turns a render result into a self-contained bash
// script (tryout-template.sh + the embedded files) that materializes the
// rendered config and runs it via docker/podman or kubectl.

// UTF-8-safe base64 for arbitrary-size strings (chunked to avoid a spread-arg
// stack overflow on large files).
function b64(str) {
  const bytes = new TextEncoder().encode(str);
  let bin = '';
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  return btoa(bin);
}
const wrap76 = (s) => s.replace(/.{1,76}/g, '$&\n').replace(/\n$/, '');

// Map the render result's flat maps/files/certs/crtLists into the on-disk tree
// HAProxy expects (paths resolve from `default-path origin /etc/haproxy`):
//   maps → maps/<name>, general files → general/<name>, certs → ssl/<name>.
// crtLists keys already carry their `general/` prefix, so keys with a '/' are
// written verbatim.
function fileList(res) {
  const files = [{ path: 'haproxy.cfg', content: res.haproxyCfg || '' }];
  const add = (obj, dir) => {
    for (const [k, v] of Object.entries(obj || {})) {
      files.push({ path: k.includes('/') ? k : `${dir}/${k}`, content: v });
    }
  };
  add(res.maps, 'maps');
  add(res.files, 'general');
  add(res.certs, 'ssl');
  add(res.crtLists, 'general');
  return files;
}

// One `mkdir -p` + base64-decode heredoc per file. The delimiter contains '_'
// which is not in the base64 alphabet, so it can never collide with content.
function writeBlock(res) {
  return fileList(res).map((f) => {
    const slash = f.path.lastIndexOf('/');
    const mk = slash > 0 ? `  mkdir -p "$WORKDIR/${f.path.slice(0, slash)}"\n` : '';
    return `${mk}  $B64D > "$WORKDIR/${f.path}" <<'__HAPTIC_B64__'\n${wrap76(b64(f.content))}\n__HAPTIC_B64__`;
  }).join('\n');
}

// Pure substitution (separated from the fetch so it's testable in Node).
export function renderTryoutScript(template, res, haproxyVersion) {
  const ver = /^[0-9][0-9.]*$/.test(haproxyVersion || '') ? haproxyVersion : '3.4';
  return template
    .replace('__VERSION__', () => ver)
    .replace('__WRITE_FILES__', () => writeBlock(res));
}

export async function buildTryoutScript(res, haproxyVersion) {
  const template = await (await fetch('./tryout-template.sh')).text();
  return renderTryoutScript(template, res, haproxyVersion);
}
