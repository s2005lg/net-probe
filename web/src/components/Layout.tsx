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
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col border-r border-edge bg-surface">
        <div className="px-4 py-5 font-head text-lg text-ok">net-probe</div>
        <nav className="flex flex-col gap-1 px-2">
          {NAV.map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                `flex items-center gap-2 rounded px-3 py-2 text-sm transition-colors focus:outline-none ${
                  isActive
                    ? "bg-panel text-ok"
                    : "text-muted hover:bg-panel hover:text-fg"
                }`
              }
            >
              <Icon size={18} />
              {label}
            </NavLink>
          ))}
        </nav>
        <button
          onClick={logout}
          className="mt-auto mb-4 mx-2 flex items-center gap-2 rounded px-3 py-2 text-sm text-muted transition-colors hover:bg-panel hover:text-danger focus:outline-none"
        >
          <LogOut size={18} />
          退出登录
        </button>
      </aside>
      <main className="flex-1 overflow-auto bg-surface p-6">
        <Outlet />
      </main>
    </div>
  );
}
