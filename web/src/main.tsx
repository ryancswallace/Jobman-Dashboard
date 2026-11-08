import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles.css";
import { PresentationProvider } from "./lib/presentation";
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <PresentationProvider>
      <App />
    </PresentationProvider>
  </StrictMode>,
);
