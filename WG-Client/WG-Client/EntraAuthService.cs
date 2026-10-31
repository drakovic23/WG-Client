using Microsoft.Identity.Client;
using Microsoft.Identity.Client.Broker;
using Microsoft.UI.Xaml;
using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using WG_Client.Core;
using WinRT.Interop;

namespace WG_Client
{
    public class EntraAuthService
    {
        const string TENANT_ID = "organizations";
        // Scopes is required and because the app registration doesn't have any permissions I believet this approach is necessary
        private string[] Scopes; // api://<api-app-client-id>

        public IPublicClientApplication _pca;
        public EntraAuthService(Window window, Connection conn)
        {
            IntPtr hwnd = WindowNative.GetWindowHandle(window);

            _pca = PublicClientApplicationBuilder.Create(conn.M365DelegateAppId)
                .WithAuthority(AzureCloudInstance.AzurePublic, TENANT_ID)
                .WithParentActivityOrWindow(() => hwnd)
                .WithBroker(new BrokerOptions(BrokerOptions.OperatingSystems.Windows)
                {
                    Title = "Test App"
                }).Build();

            Scopes = new string[] { $"api://{conn.M365ApiId}/.default" };
        }

        public IPublicClientApplication Client => _pca;

        public async Task<AuthenticationResult> GetTokensAsync(CancellationToken ct = default)
        {
            IAccount? existing = (await _pca.GetAccountsAsync()).FirstOrDefault();

            // Could try silent login here first

            try
            {
                return await _pca.AcquireTokenInteractive(Scopes).WithAccount(existing).ExecuteAsync(ct);
            }
            catch(Exception ex)
            {
                return null;
            }
        }
           

        //TODO: Add a proper disconnect/signout

        //public async Task SignOutAsync()
        //{
        //    foreach (var account in await _pca.GetAccountsAsync())
        //        await _pca.RemoveAsync(account);
        //}
    }
}
