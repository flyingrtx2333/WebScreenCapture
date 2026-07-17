using System.Threading;
using System.Windows;

namespace WebScreenCapture.Client;

public partial class App : System.Windows.Application
{
    private const string InstanceMutexName = @"Local\WebScreenCapture.Agent";
    private const string ActivationEventName = @"Local\WebScreenCapture.Agent.Activate";

    private Mutex? _singleInstanceMutex;
    private EventWaitHandle? _activationEvent;
    private CancellationTokenSource? _activationListenerCts;
    private bool _ownsInstanceMutex;

    protected override void OnStartup(StartupEventArgs e)
    {
        _activationEvent = new EventWaitHandle(false, EventResetMode.AutoReset, ActivationEventName);
        _singleInstanceMutex = new Mutex(initiallyOwned: true, InstanceMutexName, out var createdNew);
        if (!createdNew)
        {
            _activationEvent.Set();
            Shutdown();
            return;
        }
        _ownsInstanceMutex = true;

        base.OnStartup(e);
        var window = new MainWindow();
        MainWindow = window;
        window.Show();
        _activationListenerCts = new CancellationTokenSource();
        _ = Task.Run(() => ListenForActivation(_activationListenerCts.Token));
    }

    protected override void OnExit(ExitEventArgs e)
    {
        _activationListenerCts?.Cancel();
        _activationEvent?.Set();
        _activationEvent?.Dispose();
        _activationListenerCts?.Dispose();
        if (_ownsInstanceMutex) _singleInstanceMutex?.ReleaseMutex();
        _singleInstanceMutex?.Dispose();
        base.OnExit(e);
    }

    private void ListenForActivation(CancellationToken cancellationToken)
    {
        try
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                _activationEvent!.WaitOne();
                if (cancellationToken.IsCancellationRequested) return;
                Dispatcher.BeginInvoke(() => (MainWindow as MainWindow)?.RestoreFromExternalLaunch());
            }
        }
        catch (ObjectDisposedException)
        {
            // Application shutdown disposes the wait handle to release this listener.
        }
    }
}
