import Alpine from "alpinejs";
import { createImageReviewApp } from "./app.js";
import "./main.css";

window.imageReviewApp = createImageReviewApp;
Alpine.start();
