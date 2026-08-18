import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import Layout from "./components/Layout";
import Alerts from "./pages/Alerts";
import Login from "./pages/Login";
import NodeDetail from "./pages/NodeDetail";
import Nodes from "./pages/Nodes";
import Overview from "./pages/Overview";
import Settings from "./pages/Settings";
import Versions from "./pages/Versions";

export default function App() {
  return (
    <BrowserRouter>
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
    </BrowserRouter>
  );
}
