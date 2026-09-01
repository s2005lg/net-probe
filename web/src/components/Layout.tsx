import {
  Bell,
  GitCompareArrows,
  LayoutDashboard,
  LogOut,
  Server,
  Settings as SettingsIcon,
} from "lucide-react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { api } from "../lib/api";

const NAV = [
  { to: "/overview", label: "总览", icon: LayoutDashboard },
  { to: "/nodes", label: "节点", icon: Server },
  { to: "/alerts", label: "告警", icon: Bell },
  { to: "/versions", label: "版本", icon: GitCompareArrows },
  { to: "/settings", label: "设置", icon: SettingsIcon },
];

export default function Layout() {
  const navigate = useNavigate();

  async function logout() {
    try {
      await api.logout();
    } finally {
      navigate("/login");
    }
  }

  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      <a
        href="#main-content"
        className="sr-only fixed left-3 top-3 z-50 rounded bg-ok px-3 py-2 text-surface focus:not-sr-only focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-fg"
      >
        跳到主内容
      </a>
      <aside className="flex w-full shrink-0 flex-col border-b border-edge bg-surface md:w-56 md:border-r md:border-b-0">
        <div className="flex items-center justify-between md:block">
          <div className="px-4 py-4 font-head text-lg text-ok md:py-5">net-probe</div>
          <button
            onClick={logout}
            aria-label="退出登录"
            className="mr-2 inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center rounded text-muted transition-colors hover:bg-panel hover:text-danger focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok md:hidden"
          >
            <LogOut size={18} aria-hidden="true" />
          </button>
        </div>
        <nav className="flex gap-1 overflow-x-auto px-2 pb-2 md:flex-col md:overflow-visible md:pb-0">
          {NAV.map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                `flex min-h-11 shrink-0 cursor-pointer items-center gap-2 rounded px-3 py-2 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok ${
                  isActive
                    ? "bg-panel text-ok"
                    : "text-muted hover:bg-panel hover:text-fg"
                }`
              }
            >
              <Icon size={18} aria-hidden="true" />
              {label}
            </NavLink>
          ))}
        </nav>
        <button
          onClick={logout}
          className="mt-auto mb-4 mx-2 hidden min-h-11 cursor-pointer items-center gap-2 rounded px-3 py-2 text-sm text-muted transition-colors hover:bg-panel hover:text-danger focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok md:flex"
        >
          <LogOut size={18} aria-hidden="true" />
          退出登录
        </button>
      </aside>
      <main id="main-content" tabIndex={-1} className="min-w-0 flex-1 overflow-auto bg-surface p-4 sm:p-6">
        <Outlet />
      </main>
    </div>
  );
}
