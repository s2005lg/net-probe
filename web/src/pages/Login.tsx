import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../lib/api";

export default function Login() {
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api.login(username, password);
      navigate("/overview");
    } catch (err) {
      setError(err instanceof Error ? err.message : "登录失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface px-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm space-y-4 rounded-lg border border-edge bg-panel p-6"
      >
        <h1 className="font-head text-xl text-fg">net-probe 登录</h1>
        <div>
          <label className="text-sm text-muted">用户名</label>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            aria-label="用户名"
            className="mt-1 w-full rounded border border-edge bg-surface px-3 py-2 text-fg outline-none focus:border-ok"
          />
        </div>
        <div>
          <label className="text-sm text-muted">密码</label>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-label="密码"
            className="mt-1 w-full rounded border border-edge bg-surface px-3 py-2 text-fg outline-none focus:border-ok"
          />
        </div>
        {error && <p className="text-sm text-danger">{error}</p>}
        <button
          type="submit"
          disabled={busy}
          className="w-full rounded bg-ok px-3 py-2 font-medium text-surface transition-opacity hover:opacity-90 focus:outline-none disabled:opacity-50"
        >
          {busy ? "登录中…" : "登录"}
        </button>
      </form>
    </div>
  );
}
