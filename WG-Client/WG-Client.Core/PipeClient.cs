using System.Diagnostics;
using System.IO.Pipes;
using System.Net.Http.Json;
using System.Net.NetworkInformation;
using System.Security.Principal;
using WG_Client.Core;
using static System.Net.WebRequestMethods;

namespace WG_Client.WinPipe
{
    record ConnectionRequest(string AccessToken, string AuthHost, bool UseHttps, string serverPort);
    //record TestMessage(string Message);
    public class PipeClient
    {
        public async Task<bool> RequestConnection(string accessToken, Connection connection, bool useHttps = true)
        {
            var handler = new SocketsHttpHandler
            {
                ConnectCallback = async (context, cancellationToken) =>
                {
                    var pipe = new NamedPipeClientStream(".", "testPipe", PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Identification);

                    try
                    {
                        await pipe.ConnectAsync(5000, cancellationToken);
                        return pipe;
                    }
                    catch
                    {
                        pipe.Dispose();
                        throw;
                    }
                }
            };

            using var http = new HttpClient(handler) { BaseAddress = new Uri("http://pipe/") };

            var connReq = new ConnectionRequest(accessToken, connection.Host, connection.UseHttps, connection.Port);
            Debug.WriteLine($"Sending connection request to {connection.Host}:{connection.Port}");


            try
            {
                using var response = await http.PostAsJsonAsync("connect", connReq);

                if (response.StatusCode == System.Net.HttpStatusCode.NoContent)
                {
                    Console.WriteLine("Sent pipe data");
                }
                else
                {
                    var error = await response.Content.ReadAsStringAsync();
                    Console.WriteLine($"Rejected ({(int)response.StatusCode}): {error}");
                }

            }catch(Exception ex)
            {
                Debug.WriteLine(ex.Message);
                return false;
            }

            return true;
        }



        public async Task StopListenerAsync()
        {
            if (_cts is null) return;
            _cts.Cancel();
            await _loop!;
            _cts.Dispose();
            _cts = null;
            _loop = null;
        }

        public event EventHandler<string>? ServiceState;
        public TimeSpan _interval { get; set; } = TimeSpan.FromSeconds(2);
        private CancellationTokenSource? _cts;
        private Task? _loop;
        public void StartListener()
        {
            if (_loop is not null) return;
            _cts = new CancellationTokenSource();
            _loop = Task.Run(() => StartListenerAsync(_cts.Token));   // thread pool, not UI thread
        }

        private async Task StartListenerAsync(CancellationToken ct)
        {
            try
            {
                var handler = new SocketsHttpHandler
                {
                    ConnectCallback = async (context, cancellationToken) =>
                    {
                        var pipe = new NamedPipeClientStream(".", "testPipe", PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Identification);

                        try
                        {
                            await pipe.ConnectAsync(5000, cancellationToken);
                            return pipe;
                        }
                        catch
                        {
                            pipe.Dispose();
                            throw;
                        }
                    }
                };
                using var http = new HttpClient(handler) { BaseAddress = new Uri("http://pipe/") };

                using var timer = new PeriodicTimer(_interval);
                do
                {
                    try
                    {
                        //using var req = CancellationTokenSource.CreateLinkedTokenSource(ct);
                        //req.CancelAfter(TimeSpan.FromSeconds(3));
                        using var req = CancellationTokenSource.CreateLinkedTokenSource(ct);
                        req.CancelAfter(TimeSpan.FromSeconds(1));

                        var resp = await http.GetFromJsonAsync<string>("/state", req.Token);

                        //string data = await request.Content.ReadAsStringAsync();
                        Debug.WriteLine($"Got content: {resp}");

                        if (resp != null)
                        {
                            ServiceState?.Invoke(this, resp);
                        }

                    }
                    catch (Exception ex) when (!ct.IsCancellationRequested)
                    {
                        Debug.WriteLine($"Polling pipe failed: {ex}");
                    }
                }
                while (await timer.WaitForNextTickAsync(ct));
            }
            catch (OperationCanceledException) 
            {
                Debug.WriteLine("Listener has stopped");
            }
            catch (Exception ex)
            {
                Debug.WriteLine($"Listener crashed: {ex}");
            }
        }

        public async void RequestDisconnect()
        {
            var handler = new SocketsHttpHandler
            {
                ConnectCallback = async (context, cancellationToken) =>
                {
                    var pipe = new NamedPipeClientStream(".", "testPipe", PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Identification);

                    try
                    {
                        await pipe.ConnectAsync(5000, cancellationToken);
                        return pipe;
                    }
                    catch
                    {
                        pipe.Dispose();
                        throw;
                    }
                }
            };

            using var http = new HttpClient(handler) { BaseAddress = new Uri("http://pipe/") };

            try
            {
                using var response = await http.PostAsync("disconnect", null);

                if (response.StatusCode == System.Net.HttpStatusCode.NoContent)
                {
                    Console.WriteLine("Sent pipe data");
                }
                else
                {
                    var error = await response.Content.ReadAsStringAsync();
                    Console.WriteLine($"Rejected ({(int)response.StatusCode}): {error}");
                }

            }
            catch (Exception ex)
            {
                Debug.WriteLine(ex.Message);
            }
        }

    }
}
