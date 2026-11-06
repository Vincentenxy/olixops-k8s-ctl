package application

// A fixed, server-owned reader opens every component relative to an already-open
// directory, rejecting symlinks at every level and rejecting non-regular files.
// The caller supplies only an administrator-approved path as a separate argv.
// Perl/Fcntl must already exist in the target; unavailable runtimes fail closed.
const diagnosticFileReader = `use strict;
use warnings;
use Fcntl qw(O_RDONLY O_NOFOLLOW O_DIRECTORY O_NONBLOCK S_ISREG);
my $path = shift @ARGV;
my @parts = split m{/}, $path;
shift @parts;
sysopen(my $dir, "/", O_RDONLY | O_DIRECTORY | O_NOFOLLOW) or die "cannot open root\n";
for (my $i = 0; $i < @parts; $i++) {
    my $last = $i == $#parts;
    my $relative = "/proc/self/fd/" . fileno($dir) . "/" . $parts[$i];
    my $flags = O_RDONLY | O_NOFOLLOW | ($last ? O_NONBLOCK : O_DIRECTORY);
    sysopen(my $next, $relative, $flags) or die "cannot open approved path\n";
    if ($last) {
        my @st = stat($next);
        S_ISREG($st[2]) or die "regular file required\n";
        my $output = "";
        while (length($output) < 65537) {
            my $chunk;
            my $n = sysread($next, $chunk, 65537 - length($output));
            defined($n) or die "read failed\n";
            last if $n == 0;
            $output .= $chunk;
        }
        binmode STDOUT;
        print $output;
        exit 0;
    }
    $dir = $next;
}
die "empty path\n";
`
