using H.NotifyIcon;
using H.NotifyIcon.Core;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Data;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Navigation;
using Microsoft.UI.Xaml.Shapes;
using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices.WindowsRuntime;
using Windows.ApplicationModel;
using Windows.ApplicationModel.Activation;
using Windows.Foundation;
using Windows.Foundation.Collections;

// To learn more about WinUI, the WinUI project structure,
// and more about our project templates, see: http://aka.ms/winui-project-info.

namespace WG_Client
{
    /// <summary>
    /// Provides application-specific behavior to supplement the default Application class.
    /// </summary>
    public partial class App : Application
    {
        //private Window? _window;
        private TaskbarIcon? _trayIcon;
        private MainWindow? _window;
        private bool _isExiting;
        /// <summary>
        /// Initializes the singleton application object.  This is the first line of authored code
        /// executed, and as such is the logical equivalent of main() or WinMain().
        /// </summary>
        public App()
        {
            InitializeComponent();
        }

        /// <summary>
        /// Invoked when the application is launched.
        /// </summary>
        /// <param name="args">Details about the launch request and process.</param>
        //protected override void OnLaunched(Microsoft.UI.Xaml.LaunchActivatedEventArgs args)
        //{
        //    _window = new MainWindow();
        //    _window.Activate();
        //}

        protected override void OnLaunched(Microsoft.UI.Xaml.LaunchActivatedEventArgs args)
        {
            // 1. Attach logic to the commands declared in App.xaml
            var showHide = (XamlUICommand)Resources["ShowHideWindowCommand"];
            showHide.ExecuteRequested += (_, _) => ToggleMainWindow();

            var exit = (XamlUICommand)Resources["ExitApplicationCommand"];
            exit.ExecuteRequested += (_, _) => ExitApp();

            // 2. A TaskbarIcon kept in resources is never part of a visual tree,
            //    so nothing creates it automatically. ForceCreate() does that.
            //    Efficiency Mode stays off because the window is visible at startup.
            _trayIcon = (TaskbarIcon)Resources["TrayIcon"];
            _trayIcon.ForceCreate(enablesEfficiencyMode: false);

            // 3. Create the main window and turn "close" into "hide"
            _window = new MainWindow();
            _window.Closed += OnMainWindowClosed;
            _window.Activate();
        }

        private void ToggleMainWindow()
        {
            if (_window is null) return;

            if (_window.Visible)
            {
                _window.Hide();       // H.NotifyIcon extension: hides the window and enables Efficiency Mode
            }
            else
            {
                _window.Show();       // shows the window and disables Efficiency Mode
                _window.Activate();   // brings it to the foreground and gives it focus
            }
        }

        private void OnMainWindowClosed(object sender, WindowEventArgs args)
        {
            if (_isExiting) return;   // a real exit was requested, so let the window close

            args.Handled = true;      // cancel the close...
            _window!.Hide();          // ...and hide the window instead
        }

        private void ExitApp()
        {
            _isExiting = true;
            _trayIcon?.Dispose();     // removes the icon right away; otherwise a "ghost" icon stays until hovered
            _window?.Close();
            Exit();
        }
    }
}
