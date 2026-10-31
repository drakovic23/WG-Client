using Microsoft.Identity.Client;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.Windows.Storage.Pickers;
using System;
using System.Collections.ObjectModel;
using System.Diagnostics;
using System.IO;
using System.Net.Http;
using System.Runtime.InteropServices;
using WG_Client.Core;
using WG_Client.WinPipe;
using Windows.Graphics;

namespace WG_Client
{
    public sealed partial class MainWindow : Window
    {
        [DllImport("user32.dll")]
        static extern uint GetDpiForWindow(IntPtr hwnd);
        public ObservableCollection<Connection> Connections { get; } = new();
        private PipeClient _client;
        public MainWindow()
        {
            InitializeComponent();
            ConfigHandler.EnsureDataPathExists();

            _client = new();
            _client.ServiceState += (_, content) => DispatcherQueue
                    .TryEnqueue(() => OnServiceStateChanged(content));
            _client.StartListener();

            try
            {
                var cons = ConfigHandler.LoadSavedConfigs();
                if (cons.Count > 0)
                    Connections = new(cons);
            }
            catch (Exception ex)
            {
                Debug.WriteLine("Error loading saved configs");
            }


            if (AppWindow.Presenter is OverlappedPresenter presenter)
            {
                // Remove title bar
                presenter.SetBorderAndTitleBar(true, false);
                presenter.IsMaximizable = false;
                presenter.IsMinimizable = false;
                presenter.IsResizable = false;

                // Get the window handle and resize according to monitor scale
                var hwnd = WinRT.Interop.WindowNative.GetWindowHandle(this);
                double scale = GetDpiForWindow(hwnd) / 96.0;
                AppWindow.Resize(new SizeInt32((int)(350 * scale), (int)(600 * scale)));

                // Set the window position to the bottom right
                var area = DisplayArea.GetFromWindowId(AppWindow.Id, DisplayAreaFallback.Nearest).WorkArea;
                var size = AppWindow.Size;
                AppWindow.Move(new PointInt32( // Subtract the window's full size from the right and bottom edges
                    area.X + area.Width - size.Width,
                    area.Y + area.Height - size.Height));
            }
        }

        private void OnServiceStateChanged(string s)
        {
            if (CurrentConnection == null)
                return;

            ConnectionState state = StrToConnectionState(s);
            if(CurrentConnection.State != state)
            {
                CurrentConnection.State = state;
                Debug.WriteLine($"Set connection state to {state}");
            }
        }

        public ConnectionState StrToConnectionState(string s)
        {
            Debug.WriteLine($"StrToConnectionState({s})");
            switch (s)
            {
                case "disconnected":
                    return ConnectionState.Disconnected;
                case "connected":
                    return ConnectionState.Connected;
                case "connecting":
                    return ConnectionState.Connecting;
                case "error":
                case "connection_error":
                    return ConnectionState.ErrorConnecting;

                default:
                    return ConnectionState.NotAttempted;
            }
        }

        public void SetConnectionState(Connection conn, ConnectionState state)
        {
            if (conn.State == state)
                return;

            conn.State = state;
        }

        public Connection? SelectedConnection = null;
        public Connection? CurrentConnection = null;
        private void OnConnectionSelectionChanged(object sender, SelectionChangedEventArgs e)
        {
            if (ConnectionsList.SelectedItem is Connection connection)
            {
                SelectedConnection = connection;
            }
        }

        private async void OnConnectClick(object sender, RoutedEventArgs e)
        {
            ConnectButton.IsEnabled = false;

            if (SelectedConnection == null)
            {
                // No connections have been imported
                if (Connections.Count < 1)
                    return;

                // Default to the first connection
                SelectedConnection = Connections[0];
            }

            SelectedConnection.State = ConnectionState.Connecting;
            // Ask user to login to M365
            EntraAuthService authService = new(this, SelectedConnection);
            AuthenticationResult result = await authService.GetTokensAsync();

            if (result != null)
            {
                if (await _client.RequestConnection(result.AccessToken, SelectedConnection))
                {

                    CurrentConnection = SelectedConnection;

                    ConnectButton.Content = "Disconnect";
                    ConnectButton.Click -= OnConnectClick;
                    ConnectButton.Click += OnDisconnectClick;
                }
                else
                {
                    SetConnectionState(SelectedConnection, ConnectionState.ErrorConnecting);
                }
            }
            else
            {
                SetConnectionState(SelectedConnection, ConnectionState.ErrorConnecting);
            }
            ConnectButton.IsEnabled = true;
        }
        private async void OnDisconnectClick(object sender, RoutedEventArgs e)
        {
            ConnectButton.IsEnabled = false;
            ConnectButton.Click -= OnDisconnectClick;
            ConnectButton.Click += OnConnectClick;
            ConnectButton.Content = "Connect";
            // Tell the pipe to stop connection
            _client.RequestDisconnect();

            ConnectButton.IsEnabled = true;
        }

        private async void OnImportConnectionClick(object sender, RoutedEventArgs e)
        {

            FileOpenPicker picker = new FileOpenPicker(this.AppWindow.Id);
            picker.SuggestedStartLocation = PickerLocationId.Downloads;
            picker.FileTypeFilter.Add(".wgc");

            PickFileResult result = await picker.PickSingleFileAsync();

            if (result is null) // User cancelled file selection
            {
                return;
            }

#if DEBUG
            PrintDataLoc();
#endif
            Connection? newConnection = ConfigHandler.ImportConfig(result.Path);

            if (newConnection != null)
            {
                for (int i = 0; i < Connections.Count; i++)
                {
                    if (Connections[i].Host == newConnection.Host)
                    {
                        Connections.RemoveAt(i);
                    }
                }
                Connections.Add(newConnection);
            }
            else
            {
                ShowBlockingPopup("Error importing config", "Invalid config");
            }
        }

        private async void OnAboutClick(object sender, RoutedEventArgs e)
        {

        }
        // Left over from testing stuff
        private async void ShowBlockingPopup(string message, string title)
        {
            ContentDialog errorDialog = new ContentDialog
            {
                XamlRoot = this.Content.XamlRoot,
                Title = title,
                Content = message,
                CloseButtonText = "OK"
            };

            await errorDialog.ShowAsync();
        }


#if DEBUG
        public void PrintDataLoc()
        {
            try
            {
                var actualPath = Path.Combine(
                    Windows.Storage.ApplicationData.Current.LocalCacheFolder.Path,
                    "Local", ConfigHandler.DATA_FOLDER_NAME);
                Debug.WriteLine($"Data directory (on disk): {actualPath}");
            }
            catch (Exception)
            {
                // No package identity (running unpackaged), so there's no redirection
            }
        }
#endif
    }
}
