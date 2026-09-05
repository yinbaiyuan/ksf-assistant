const args = process.argv.slice(2);
const command = args[1];
if (command === 'offline') { process.stderr.write('service unavailable: isolated fixture'); process.exitCode = 1; }
else if (command === 'invalid') process.stdout.write('not JSON');
else if (command === 'approval') { process.stdout.write(JSON.stringify({ status: 'authorization_required', challenge: 'isolated-challenge' })); process.exitCode = 1; }
else if (command === 'rejected') process.stdout.write(JSON.stringify({ status: 'rejected', errorCode: 'capability_disabled' }));
else if (command === 'huge') process.stdout.write('x'.repeat(17 * 1024 * 1024));
else if (command === 'wait') setInterval(() => {}, 1000);
else {
  let input = '';
  for await (const chunk of process.stdin) input += chunk;
  process.stdout.write(JSON.stringify({ args, input }));
}
