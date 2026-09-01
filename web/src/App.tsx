import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import Layout from "./components/Layout";

const Alerts = lazy(() => import("./pages/Alerts"));
const Login = lazy(() => import("./pages/Login"));
const NodeDetail = lazy(() => import("./pages/NodeDetail"));
const Nodes = lazy(() => import("./pages/Nodes"));
const Overview = lazy(() => import("./pages/Overview"));
const Settings = lazy(() => import("./pages/Settings"));
const Versions = lazy(() => import("./pages/Versions"));

export default function App() {
  return (
    <BrowserRouter>
      <Suspense fallback={<div className="p-6 text-slate-400">加载中…</div>}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route element={<Layout />}>
            <Route path="/overview" element={<Overview />} />
            <Route path="/nodes" element={<Nodes />} />
            <Route path="/nodes/:id" element={<NodeDetail />} />
            <Route path="/alerts" element={<Alerts />} />
            <Route path="/versions" element={<Versions />} />
            <Route path="/settings" element={<Settings />} />
          </Route>
          <Route path="*" element={<Navigate to="/overview" replace />} />
        </Routes>
      </Suspense>
    </BrowserRouter>
  );
}
