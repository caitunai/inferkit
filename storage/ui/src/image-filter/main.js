import Alpine from "alpinejs";
import { createImageFilterApp } from "./app.js";
import "./main.css";

window.imageFilterApp = createImageFilterApp;
Alpine.start();
