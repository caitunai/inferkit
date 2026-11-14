import Alpine from "alpinejs";
import { createReclassificationApp } from "./reclassify.js";
import "./main.css";

window.reclassificationApp = createReclassificationApp;
Alpine.start();
