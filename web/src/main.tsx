import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { captureError, initializeTelemetry } from "./telemetry";
import "./app.css";

void initializeTelemetry();
const root = document.getElementById("root");
if (!root) {
  throw new Error("no #root to mount into");
}

createRoot(root, { onCaughtError: captureError, onUncaughtError: captureError, onRecoverableError: captureError }).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
