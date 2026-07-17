using System.ComponentModel;
using System.Windows;

namespace WebScreenCapture.Client;

public partial class MainWindow : Window
{
    private readonly SettingsStore _settingsStore = new();
    private System.Windows.Forms.NotifyIcon? _trayIcon;
    private System.Drawing.Icon? _applicationIcon;
    private NativeStreamingAgent? _agent;
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

    private void MainWindow_Loaded(object sender, RoutedEventArgs e)
    {
        try
        {
            var settings = _settingsStore.Load();
            if (settings is null) return;
            ServerUrlInput.Text = settings.ServerUrl;
            PairingTokenInput.Text = settings.PairingToken;
        }
        catch (Exception ex)
        {
            SetupError.Text = $"读取连接设置失败：{ex.Message}";
        }
    }

    private async void StartButton_Click(object sender, RoutedEventArgs e)
    {
        SetupError.Text = string.Empty;
        StartButton.IsEnabled = false;
        try
        {
            var serverUrl = ServerAddress.Normalize(ServerUrlInput.Text);
            var token = PairingTokenInput.Text.Trim();
            if (token.Length == 0) throw new InvalidOperationException("请输入与观看网页相同的配对 Token。");

            var settings = new AgentSettings(serverUrl, token);
            _settingsStore.Save(settings);
            var agent = new NativeStreamingAgent(settings);
            agent.StatusChanged += Agent_StatusChanged;
            agent.CaptureStatsChanged += Agent_CaptureStatsChanged;
            _agent = agent;
            SetInputsEnabled(false);
            await agent.StartAsync();
            StartButton.Visibility = Visibility.Collapsed;
            StopButton.Visibility = Visibility.Visible;
            var screen = System.Windows.Forms.Screen.PrimaryScreen?.Bounds;
            ResolutionMetric.Text = screen is null ? "主桌面" : $"{screen.Value.Width}×{screen.Value.Height}";
        }
        catch (Exception ex)
        {
            SetupError.Text = ex.Message;
            await DisposeAgentAsync();
            SetInputsEnabled(true);
            RuntimeState.Text = "启动失败";
            RuntimeDetail.Text = "请检查 NVIDIA 驱动、网络和发布文件是否完整。";
        }
        finally
        {
            StartButton.IsEnabled = true;
        }
    }

    private async void StopButton_Click(object sender, RoutedEventArgs e)
    {
        StopButton.IsEnabled = false;
        await DisposeAgentAsync();
        StopButton.Visibility = Visibility.Collapsed;
        StartButton.Visibility = Visibility.Visible;
        SetInputsEnabled(true);
        ResetMetrics();
        StopButton.IsEnabled = true;
    }

    private void Agent_StatusChanged(AgentRuntimeStatus status)
    {
        Dispatcher.Invoke(() =>
        {
            RuntimeState.Text = status.State;
            RuntimeDetail.Text = status.Detail;
            ViewerMetric.Text = status.ViewerConnected ? "观看端在线" : "观看端离线";
            ViewerMetric.Foreground = status.ViewerConnected
                ? (System.Windows.Media.Brush)FindResource("AccentBrush")
                : (System.Windows.Media.Brush)FindResource("MutedBrush");
        });
    }

    private void Agent_CaptureStatsChanged(CaptureSnapshot snapshot)
    {
        Dispatcher.Invoke(() =>
        {
            FpsMetric.Text = $"{snapshot.FramesPerSecond:F0} fps";
            BitrateMetric.Text = FormatBitrate(snapshot.BitsPerSecond);
        });
    }

    private void ResetButton_Click(object sender, RoutedEventArgs e)
    {
        if (_agent?.IsRunning == true) return;
        _settingsStore.Delete();
        PairingTokenInput.Text = string.Empty;
        SetupError.Text = string.Empty;
        PairingTokenInput.Focus();
    }

    private void SetInputsEnabled(bool enabled)
    {
        ServerUrlInput.IsEnabled = enabled;
        PairingTokenInput.IsEnabled = enabled;
        ResetButton.IsEnabled = enabled;
    }

    private void ResetMetrics()
    {
        RuntimeState.Text = "已停止";
        RuntimeDetail.Text = "点击开始后，程序会直接捕获整个主桌面。";
        ViewerMetric.Text = "观看端离线";
        ResolutionMetric.Text = "—";
        FpsMetric.Text = "—";
        BitrateMetric.Text = "—";
    }

    private async Task DisposeAgentAsync()
    {
        var agent = _agent;
        _agent = null;
        if (agent is null) return;
        agent.StatusChanged -= Agent_StatusChanged;
        agent.CaptureStatsChanged -= Agent_CaptureStatsChanged;
        await agent.DisposeAsync();
    }

    private void InitializeTrayIcon()
    {
        var menu = new System.Windows.Forms.ContextMenuStrip();
        menu.Items.Add("打开", null, (_, _) => RestoreWindow());
        menu.Items.Add(new System.Windows.Forms.ToolStripSeparator());
        menu.Items.Add("退出", null, ExitMenuItem_Click);

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
            Topmost = true;
            Topmost = false;
            Focus();
        });
    }

    internal void RestoreFromExternalLaunch() => RestoreWindow();

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
            _trayIcon.ShowBalloonTip(2500, "Screen Capture", "原生桌面捕获仍在运行，可从托盘重新打开。", System.Windows.Forms.ToolTipIcon.Info);
            _shownTrayHint = true;
        }
    }

    private async Task ExitApplicationAsync()
    {
        if (_exitRequested) return;
        _exitRequested = true;
        try
        {
            await DisposeAgentAsync();
        }
        finally
        {
            if (_trayIcon is not null) _trayIcon.Visible = false;
            _trayIcon?.Dispose();
            _applicationIcon?.Dispose();
            Close();
            System.Windows.Application.Current.Shutdown();
        }
    }

    private async void ExitMenuItem_Click(object? sender, EventArgs e)
    {
        await Dispatcher.InvokeAsync(() => ExitApplicationAsync()).Task.Unwrap();
    }

    private static string FormatBitrate(double value)
    {
        return value >= 1_000_000 ? $"{value / 1_000_000:F1} Mbps" : $"{value / 1000:F0} Kbps";
    }
}
