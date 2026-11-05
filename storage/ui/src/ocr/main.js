import Alpine from "alpinejs";
import { createOCRApp } from "./app.js";
import "./main.css";

window.ocrApp = createOCRApp;
Alpine.start();
