import { cpSync, mkdirSync } from 'node:fs';

mkdirSync('dist', { recursive: true });
cpSync('index.html', 'dist/index.html');
cpSync('style.css', 'dist/style.css');
cpSync('src/main.js', 'dist/main.js');
console.log('built web/dist');
