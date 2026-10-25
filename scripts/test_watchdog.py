import contextlib, io, tempfile, unittest
from pathlib import Path
from unittest.mock import patch, MagicMock
from watchdog import record
class MonitorDelivery(unittest.TestCase):
 def test_deduplicate_retry_and_recovery(self):
  with tempfile.TemporaryDirectory() as temp,contextlib.redirect_stdout(io.StringIO()):
   root=Path(temp)
   self.assertEqual(record(root,['atlas:HEALTH_UNAVAILABLE']),2)
   self.assertEqual(record(root,['atlas:HEALTH_UNAVAILABLE']),2)
   self.assertEqual(len((root/'alerts.jsonl').read_text().splitlines()),1)
   opener=MagicMock();opener.open.side_effect=OSError('offline')
   with patch('urllib.request.build_opener',return_value=opener):self.assertEqual(record(root,['atlas:HEALTH_UNAVAILABLE'],'https://notify.example.test'),2)
   opener.open.side_effect=None;opener.open.return_value.__enter__.return_value.status=204
   with patch('urllib.request.build_opener',return_value=opener):
    self.assertEqual(record(root,['atlas:HEALTH_UNAVAILABLE'],'https://notify.example.test'),1)
    count=opener.open.call_count
    self.assertEqual(record(root,['atlas:HEALTH_UNAVAILABLE'],'https://notify.example.test'),1);self.assertEqual(opener.open.call_count,count)
    self.assertEqual(record(root,[],'https://notify.example.test'),0);self.assertEqual(opener.open.call_count,count+1)
   self.assertEqual(len((root/'alerts.jsonl').read_text().splitlines()),2)
if __name__=='__main__':unittest.main()
