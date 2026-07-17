using Microsoft.Web.WebView2.Core;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Windows;

namespace WebScreenCapture.Client;

public partial class MainWindow : Window
{
    private readonly SettingsStore _settingsStore = new();
    private System.Windows.Forms.NotifyIcon? _trayIcon;
    private System.Drawing.Icon? _applicationIcon;
    private AgentSettings? _settings;
    private bool _webViewInitialized;
    private bool _exitRequested;
    private bool _shownTrayHint;

    public MainWindow()
    {
        InitializeComponent();
        Loaded += MainWindow_Loaded;
        StateChanged += MainWindow_StateChanged;
        Closing += MainWindow_Closing;
        InitializeTrayIcon();
    }

    private async void MainWindow_Loaded(object sender, RoutedEventArgs e)
    {
        try
        {
            _settings = _settingsStore.Load();
            if (_settings is not null)
            {
                ServerUrlInput.Text = _settings.ServerUrl;
                DeviceTokenInput.Password = _settings.DeviceToken;
                await ShowAgentAsync();
            }
        }
        catch (Exception ex)
        {
            SetupError.Text = $"读取连接设置失败：{ex.Message}";
            ShowSetup();
        }
    }

    private async void SaveButton_Click(object sender, RoutedEventArgs e)
    {
        SetupError.Text = string.Empty;
        WebView2InstallButton.Visibility = Visibility.Collapsed;
        SaveButton.IsEnabled = false;
        try
        {
            var serverUrl = ServerAddress.Normalize(ServerUrlInput.Text);
            var token = DeviceTokenInput.Password.Trim();
            if (token.Length < 32)
            {
                throw new InvalidOperationException("设备令牌长度不正确。请输入部署时生成的完整令牌。");
            }

            _settings = new AgentSettings(serverUrl, token);
            _settingsStore.Save(_settings);
            await ShowAgentAsync();
        }
        catch (Exception ex)
        {
            SetupError.Text = ex.Message;
        }
        finally
        {
            SaveButton.IsEnabled = true;
        }
    }

    private async Task ShowAgentAsync()
    {
        if (_settings is null) return;

        try
        {
            _ = CoreWebView2Environment.GetAvailableBrowserVersionString();
        }
        catch (WebView2RuntimeNotFoundException)
        {
            WebView2InstallButton.Visibility = Visibility.Visible;
            throw new InvalidOperationException("未安装 Microsoft Edge WebView2 Runtime。请从 https://go.microsoft.com/fwlink/p/?LinkId=2124703 安装后重试。");
        }

        SetupPanel.Visibility = Visibility.Collapsed;
        BrowserPanel.Visibility = Visibility.Visible;

        if (!_webViewInitialized)
        {
            var userDataFolder = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "WebScreenCapture",
                "WebView2");
            Directory.CreateDirectory(userDataFolder);
            var environment = await CoreWebView2Environment.CreateAsync(userDataFolder: userDataFolder);
            await AgentWebView.EnsureCoreWebView2Async(environment);
            AgentWebView.CoreWebView2.Settings.AreDefaultContextMenusEnabled = false;
            AgentWebView.CoreWebView2.Settings.AreDevToolsEnabled = false;
            AgentWebView.CoreWebView2.Settings.IsStatusBarEnabled = false;
            AgentWebView.CoreWebView2.Settings.IsZoomControlEnabled = false;
            AgentWebView.CoreWebView2.Settings.AreBrowserAcceleratorKeysEnabled = false;
            AgentWebView.CoreWebView2.ScreenCaptureStarting += (_, args) => args.Cancel = false;
            AgentWebView.CoreWebView2.WebMessageReceived += CoreWebView2_WebMessageReceived;
            AgentWebView.NavigationCompleted += AgentWebView_NavigationCompleted;
            _webViewInitialized = true;
        }

        var target = new Uri(new Uri(_settings.ServerUrl + "/"), "agent");
        if (AgentWebView.Source == target) AgentWebView.Reload();
        else AgentWebView.Source = target;
    }

    private void AgentWebView_NavigationCompleted(object? sender, CoreWebView2NavigationCompletedEventArgs e)
    {
        if (e.IsSuccess) return;
        Dispatcher.Invoke(() =>
        {
            SetupError.Text = $"无法打开捕获服务：{e.WebErrorStatus}。请检查服务器地址和网络。";
            ShowSetup();
        });
    }

    private void CoreWebView2_WebMessageReceived(object? sender, CoreWebView2WebMessageReceivedEventArgs e)
    {
        try
        {
            using var document = JsonDocument.Parse(e.WebMessageAsJson);
            var root = document.RootElement;
            if (!root.TryGetProperty("type", out var typeElement)) return;
            var type = typeElement.GetString();
            if (type == "ready")
            {
                PostBootstrap();
            }
            else if (type == "auth-error")
            {
                Dispatcher.Invoke(() =>
                {
                    SetupError.Text = "设备令牌验证失败，请重新输入部署时生成的令牌。";
                    ShowSetup();
                });
            }
        }
        catch (JsonException)
        {
            // Ignore messages that are not part of the host protocol.
        }
    }

    private void PostBootstrap()
    {
        if (_settings is null || AgentWebView.CoreWebView2 is null) return;
        var payload = JsonSerializer.Serialize(new { type = "bootstrap", token = _settings.DeviceToken });
        AgentWebView.CoreWebView2.PostWebMessageAsJson(payload);
    }

    private void ShowSetup()
    {
        BrowserPanel.Visibility = Visibility.Collapsed;
        SetupPanel.Visibility = Visibility.Visible;
        Activate();
        Show();
        WindowState = WindowState.Normal;
        DeviceTokenInput.Focus();
    }

    private void ResetConnectionSettings()
    {
        if (AgentWebView.CoreWebView2 is not null)
        {
            AgentWebView.CoreWebView2.CookieManager.DeleteAllCookies();
        }
        _settingsStore.Delete();
        _settings = null;
        if (_webViewInitialized) AgentWebView.Source = new Uri("about:blank");
        DeviceTokenInput.Password = string.Empty;
        SetupError.Text = string.Empty;
        ShowSetup();
    }

    private void WebView2InstallButton_Click(object sender, RoutedEventArgs e)
    {
        Process.Start(new ProcessStartInfo
        {
            FileName = "https://go.microsoft.com/fwlink/p/?LinkId=2124703",
            UseShellExecute = true,
        });
    }

    private void InitializeTrayIcon()
    {
        var menu = new System.Windows.Forms.ContextMenuStrip();
        menu.Items.Add("打开", null, (_, _) => RestoreWindow());
        menu.Items.Add("连接设置", null, (_, _) => Dispatcher.Invoke(ResetConnectionSettings));
        menu.Items.Add(new System.Windows.Forms.ToolStripSeparator());
        menu.Items.Add("退出", null, (_, _) => Dispatcher.Invoke(ExitApplication));

        var executablePath = Environment.ProcessPath;
        if (!string.IsNullOrWhiteSpace(executablePath))
        {
            using var associatedIcon = System.Drawing.Icon.ExtractAssociatedIcon(executablePath);
            _applicationIcon = associatedIcon is null
                ? (System.Drawing.Icon)System.Drawing.SystemIcons.Application.Clone()
                : (System.Drawing.Icon)associatedIcon.Clone();
        }
        else
        {
            _applicationIcon = (System.Drawing.Icon)System.Drawing.SystemIcons.Application.Clone();
        }

        _trayIcon = new System.Windows.Forms.NotifyIcon
        {
            Text = "Screen Capture",
            Icon = _applicationIcon,
            ContextMenuStrip = menu,
            Visible = true,
        };
        _trayIcon.DoubleClick += (_, _) => RestoreWindow();
    }

    private void RestoreWindow()
    {
        Dispatcher.Invoke(() =>
        {
            Show();
            WindowState = WindowState.Normal;
            Activate();
        });
    }

    private void MainWindow_StateChanged(object? sender, EventArgs e)
    {
        if (WindowState == WindowState.Minimized) HideToTray();
    }

    private void MainWindow_Closing(object? sender, CancelEventArgs e)
    {
        if (_exitRequested) return;
        e.Cancel = true;
        HideToTray();
    }

    private void HideToTray()
    {
        Hide();
        if (!_shownTrayHint && _trayIcon is not null)
        {
            _trayIcon.ShowBalloonTip(2500, "Screen Capture", "捕获器仍在运行，可从托盘重新打开。", System.Windows.Forms.ToolTipIcon.Info);
            _shownTrayHint = true;
        }
    }

    private void ExitApplication()
    {
        _exitRequested = true;
        _trayIcon?.Dispose();
        _applicationIcon?.Dispose();
        Close();
        System.Windows.Application.Current.Shutdown();
    }
}
