// main.js - Electron 主进程入口
// 启动内嵌本地服务并打开编辑器窗口

const { app, BrowserWindow, shell } = require('electron');
const path = require('path');
const fs = require('fs');
const { startServer } = require('./server');

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
    // 静态资源目录: 打包后位于 resources, 开发时位于项目根目录
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

    const port = await startServer({ webDir, assetsDir });

    win = new BrowserWindow({
      width: 1440,
      height: 900,
      title: 'LOL 一图流编辑器',
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

    await win.loadURL(`http://127.0.0.1:${port}`);
  }

  app.whenReady().then(createWindow);

  app.on('window-all-closed', () => {
    app.quit();
  });
}
