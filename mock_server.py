# mock_server.py
from http.server import HTTPServer, BaseHTTPRequestHandler
import logging


class SimpleHTTPRequestHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        content_length = int(self.headers["Content-Length"])
        body = self.rfile.read(content_length)
        logging.info(
            f"\n--- RECEIVED REQUEST ---\nPath: {self.path}\nHeaders: {self.headers}Body:\n{body.decode('utf-8')}\n----------------------"
        )
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"Request received successfully")


logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(message)s")
httpd = HTTPServer(("localhost", 8081), SimpleHTTPRequestHandler)
logging.info("Mock Downstream Server listening on http://localhost:8081")
httpd.serve_forever()
