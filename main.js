// main.js - Electron 主进程入口
// 启动内嵌本地服务并打开编辑器窗口

const { app, BrowserWindow, shell } = require('electron');
const path = require('path');
const fs = require('fs');
const { startServer } = require('./server');

// 应用内加载页 (Electron 已启动、服务尚未就绪时立即展示)
const loadingHtml = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  html, body { width: 100%; height: 100%; overflow: hidden; }
  body {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    font-family: "Microsoft YaHei", "Segoe UI", sans-serif;
    background: linear-gradient(160deg, #2c4f7d 0%, #1a3559 50%, #0d2138 100%);
    color: #e8ecf2;
  }
  .logo { font-size: 64px; color: #d9b86a; margin-bottom: 22px; filter: drop-shadow(0 4px 12px rgba(0,0,0,.45)); }
  .name { font-size: 26px; font-weight: 700; letter-spacing: 3px; color: #e6d3a0; margin-bottom: 26px; }
  .spinner {
    width: 42px; height: 42px; border-radius: 50%;
    border: 4px solid rgba(255,255,255,.15); border-top-color: #d9b86a;
    animation: spin 0.9s linear infinite; margin-bottom: 18px;
  }
  .tip { font-size: 14px; color: #9fb4cc; letter-spacing: 1px; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
</head>
<body>
  <div class="logo">⚔</div>
  <div class="name">英雄联盟一图流编辑器</div>
  <div class="spinner"></div>
  <div class="tip">正在加载编辑器资源，请稍候…</div>
</body>
</html>`;

// 启动失败时的错误页 (避免一直卡在加载中)
function errorHtml(err) {
  const detail = String((err && (err.stack || err.message)) || err)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">
<style>
  body{margin:0;padding:40px;font-family:"Microsoft YaHei",Consolas,monospace;background:#1a2332;color:#e8b4b4;font-size:13px;line-height:1.7;}
  h2{color:#ff8a6a;margin-bottom:16px;} pre{white-space:pre-wrap;word-break:break-all;}
</style></head><body><h2>启动失败</h2><pre>${detail}</pre></body></html>`;
}

const dataUrl = (html) => 'data:text/html;charset=utf-8,' + encodeURIComponent(html);

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  // 已有实例运行, 直接退出
  app.quit();
} else {
  let win = null;

  app.on('second-instance', () => {
    if (win) {
      if (win.isMinimized()) win.restore();
      win.show();
      win.focus();
    }
  });

  async function createWindow() {
    // 1) 窗口立即创建并显示加载页, 不等资源与服务
    win = new BrowserWindow({
      width: 1440,
      height: 900,
      title: 'LOL 一图流编辑器',
      backgroundColor: '#0d2138',
      autoHideMenuBar: true,
      webPreferences: {
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
      },
    });

    // 外部链接用系统默认浏览器打开
    win.webContents.setWindowOpenHandler(({ url }) => {
      shell.openExternal(url);
      return { action: 'deny' };
    });

    await win.loadURL(dataUrl(loadingHtml));

    try {
      // 2) 静态资源目录: 打包后位于 resources, 开发时位于项目根目录
      const baseDir = app.isPackaged ? process.resourcesPath : __dirname;
      const webDir = path.join(baseDir, 'web');
      let assetsDir = path.join(baseDir, 'assets');
      if (app.isPackaged) {
        // 用户数据目录持久化 (升级/重装不丢失图标与配置)
        assetsDir = path.join(app.getPath('userData'), 'assets');
        if (!fs.existsSync(assetsDir)) {
          // 首次启动: 复制应用自带的图标资源, 开箱即用
          fs.cpSync(path.join(baseDir, 'assets'), assetsDir, { recursive: true });
        }
      }

      // 3) 启动内嵌本地服务
      const port = await startServer({ webDir, assetsDir });

      // 4) 进入编辑器
      await win.loadURL(`http://127.0.0.1:${port}`);
    } catch (err) {
      // 启动异常时展示错误而不是无限转圈
      await win.loadURL(dataUrl(errorHtml(err)));
    }
  }

  app.whenReady().then(createWindow);

  app.on('window-all-closed', () => {
    app.quit();
  });
}
