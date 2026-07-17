using System.Threading;
using System.Windows;

namespace WebScreenCapture.Client;

public partial class App : System.Windows.Application
{
    private Mutex? _singleInstanceMutex;

    protected override void OnStartup(StartupEventArgs e)
    {
        _singleInstanceMutex = new Mutex(initiallyOwned: true, @"Local\WebScreenCapture.Agent", out var createdNew);
        if (!createdNew)
        {
            System.Windows.MessageBox.Show(
                "屏幕捕获器已经在运行，请检查任务栏托盘。",
                "Screen Capture",
                MessageBoxButton.OK,
                MessageBoxImage.Information);
            Shutdown();
            return;
        }

        base.OnStartup(e);
        var window = new MainWindow();
        MainWindow = window;
        window.Show();
    }

    protected override void OnExit(ExitEventArgs e)
    {
        _singleInstanceMutex?.ReleaseMutex();
        _singleInstanceMutex?.Dispose();
        base.OnExit(e);
    }
}

