import { useEffect, useState } from "react";
import { api, type SettingsData } from "../lib/api";

export default function SettingsPage() {
  const [data, setData] = useState<SettingsData | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [form, setForm] = useState({
    node_timeout: "",
    raw_days: 7,
    hourly_days: 30,
    daily_days: 365,
    cert_expiry_days: 7,
    disk_usage_pct: 85,
    mem_usage_pct: 90,
    telegram_token: "",
    telegram_chat_id: "",
    webhook_url: "",
  });

  useEffect(() => {
    api.settings()
      .then((value) => {
        setData(value);
        setForm({
          node_timeout: value.node_timeout,
          raw_days: value.retention.raw_days,
          hourly_days: value.retention.hourly_days,
          daily_days: value.retention.daily_days,
          cert_expiry_days: value.alert.cert_expiry_days,
          disk_usage_pct: value.alert.disk_usage_pct,
          mem_usage_pct: value.alert.mem_usage_pct,
          telegram_token: value.alert.telegram_token,
          telegram_chat_id: value.alert.telegram_chat_id,
          webhook_url: value.alert.webhook_url,
        });
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  function update(key: string, value: string | number) {
    setForm((prev) => ({ ...prev, [key]: value }));
    setSaved(false);
  }

  async function save() {
    setSaving(true);
    setError("");
    try {
      const next = await api.patchSettings({
        node_timeout: form.node_timeout,
        retention: {
          raw_days: Number(form.raw_days),
          hourly_days: Number(form.hourly_days),
          daily_days: Number(form.daily_days),
        },
        alert: {
          cert_expiry_days: Number(form.cert_expiry_days),
          disk_usage_pct: Number(form.disk_usage_pct),
          mem_usage_pct: Number(form.mem_usage_pct),
          telegram_token: form.telegram_token,
          telegram_chat_id: form.telegram_chat_id,
          webhook_url: form.webhook_url,
        },
      });
      setData(next);
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  if (error) return <p className="text-danger">{error}</p>;
  if (!data) return <p className="text-muted">加载中…</p>;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="font-head text-lg text-fg">设置</h1>
        <button
          type="button"
          disabled={saving}
          onClick={save}
          className="rounded bg-ok px-4 py-2 text-sm text-surface transition-opacity hover:opacity-90 disabled:opacity-50"
        >
          {saving ? "保存中…" : "保存"}
        </button>
      </div>
      {saved ? <p className="text-ok">设置已保存</p> : null}

      <div className="grid gap-4 md:grid-cols-2">
        <section className="rounded border border-edge bg-panel p-4">
          <h2 className="mb-3 font-head text-fg">服务</h2>
          <Field label="监听地址" value={data.listen_addr} disabled />
          <Field label="数据目录" value={data.data_dir} disabled />
        </section>

        <section className="rounded border border-edge bg-panel p-4">
          <h2 className="mb-3 font-head text-fg">告警</h2>
          <Field label="节点超时" value={form.node_timeout} onChange={(v) => update("node_timeout", v)} />
          <Field label="证书过期阈值（天）" value={String(form.cert_expiry_days)} type="number" onChange={(v) => update("cert_expiry_days", Number(v))} />
          <Field label="磁盘使用率阈值（%）" value={String(form.disk_usage_pct)} type="number" onChange={(v) => update("disk_usage_pct", Number(v))} />
          <Field label="内存使用率阈值（%）" value={String(form.mem_usage_pct)} type="number" onChange={(v) => update("mem_usage_pct", Number(v))} />
        </section>

        <section className="rounded border border-edge bg-panel p-4">
          <h2 className="mb-3 font-head text-fg">数据保留</h2>
          <Field label="原始（天）" value={String(form.raw_days)} type="number" onChange={(v) => update("raw_days", Number(v))} />
          <Field label="小时（天）" value={String(form.hourly_days)} type="number" onChange={(v) => update("hourly_days", Number(v))} />
          <Field label="天（天）" value={String(form.daily_days)} type="number" onChange={(v) => update("daily_days", Number(v))} />
        </section>

        <section className="rounded border border-edge bg-panel p-4">
          <h2 className="mb-3 font-head text-fg">通知</h2>
          <Field label="Telegram Token" value={form.telegram_token} onChange={(v) => update("telegram_token", v)} />
          <Field label="Telegram Chat ID" value={form.telegram_chat_id} onChange={(v) => update("telegram_chat_id", v)} />
          <Field label="Webhook URL" value={form.webhook_url} onChange={(v) => update("webhook_url", v)} />
        </section>
      </div>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  type = "text",
  disabled = false,
}: {
  label: string;
  value: string;
  onChange?: (value: string) => void;
  type?: string;
  disabled?: boolean;
}) {
  return (
    <label className="mb-3 block text-sm">
      <span className="mb-1 block text-muted">{label}</span>
      <input
        type={type}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange?.(e.target.value)}
        className="w-full rounded border border-edge bg-surface px-3 py-2 text-sm text-fg outline-none focus:border-ok disabled:opacity-60"
      />
    </label>
  );
}
