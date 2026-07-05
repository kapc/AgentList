async function api(path, options = {}) {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
    ...options,
  });
  let body = {};
  const text = await res.text();
  if (text) {
    try { body = JSON.parse(text); } catch { body = { error: text }; }
  }
  return { ok: res.ok, status: res.status, body };
}

function bindForm(id, onSubmit) {
  const form = document.getElementById(id);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const data = Object.fromEntries(new FormData(form).entries());
    await onSubmit(data);
  });
}

function showMsg(id, kind, text) {
  const el = document.getElementById(id);
  el.hidden = false;
  el.className = 'msg ' + kind;
  el.textContent = text;
}

const GOOGLE_ICON = `<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#4285F4" d="M22 12c0-.68-.06-1.34-.17-1.97H12v3.73h5.76a4.7 4.7 0 0 1-2.04 3.08v2.56h3.3C21.4 18.2 22 15.27 22 12z"/><path fill="#34A853" d="M12 23c2.7 0 4.96-.9 6.62-2.44l-3.3-2.56c-.9.6-2.04.96-3.32.96-2.56 0-4.74-1.73-5.52-4.06H2.5v2.64A10 10 0 0 0 12 23z"/><path fill="#FBBC05" d="M6.48 13.9c-.22-.6-.35-1.24-.35-1.9s.13-1.3.35-1.9V7.46H2.5A10 10 0 0 0 2 12c0 1.61.39 3.14 1.08 4.44l3.4-2.54z"/><path fill="#EA4335" d="M12 5.38c1.47 0 2.78.5 3.81 1.48l2.85-2.85C16.95 2.6 14.7 1.8 12 1.8 7.7 1.8 4.02 4.48 2.5 7.46l3.98 3.08C7.26 8.11 9.44 5.38 12 5.38z"/></svg>`;

async function setupGoogleAuth(buttonId, label) {
  const btn = document.getElementById(buttonId);
  if (!btn) return;

  const res = await api('/api/v1/auth/config');
  if (!res.ok || !res.body.google_oauth_enabled) {
    btn.hidden = true;
    const divider = document.getElementById('google-divider');
    if (divider) divider.hidden = true;
    const hint = document.getElementById('google-hint');
    if (hint) {
      hint.hidden = false;
      hint.textContent = 'Google sign-in is off. Add GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET to .env, set the redirect URI in Google Cloud Console, then restart the server.';
    }
    return;
  }

  const hint = document.getElementById('google-hint');
  if (hint) hint.hidden = true;

  const text = label || 'Continue with Google';
  btn.innerHTML = GOOGLE_ICON + '<span>' + text + '</span>';
  btn.hidden = false;
  btn.addEventListener('click', () => {
    window.location.href = '/api/v1/auth/google';
  });
}

function showQueryError(paramId) {
  const params = new URLSearchParams(window.location.search);
  const err = params.get('error');
  if (!err) return;
  const messages = {
    google_failed: 'Google sign-in failed. Please try again.',
    google_disabled: 'Google sign-in is not configured on this server.',
  };
  showMsg(paramId, 'error', messages[err] || 'Sign-in failed.');
}
