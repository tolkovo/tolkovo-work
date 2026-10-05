import { options } from "preact";

// Preact runs effects after the next frame: requestAnimationFrame, or 100 ms
// without one. In jsdom that depends on the machine's speed, so a slow CI
// runner checked the DOM before an effect (a preview, a returned focus) ran.
// Here effects run in the next task, which settle() in the tests waits for.
options.requestAnimationFrame = (cb) => void setTimeout(cb, 0);
